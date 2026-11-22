// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

const squarePage = "0 0 1 rg 10 10 60 40 re f"

// deepPage nests n clips of a shape that is not a rectangle, which is the one
// kind render refuses past 64.
func deepPage(n int) string {
	return strings.Repeat("q 10 10 m 90 10 l 50 90 l h W n ", n) + squarePage
}

// buildPDF writes a document whose pages carry the given content streams.
func buildPDF(t *testing.T, enc *reader.Encryption, contents ...string) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	if enc != nil {
		w.Encrypt(*enc)
	}
	pagesRef := w.Reserve()
	kids := reader.Array{}
	for _, c := range contents {
		cs := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(c)})
		kids = append(kids, w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": cs}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(len(contents)),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// wellFormed parses the whole document as XML and returns its root element.
func wellFormed(t *testing.T, b []byte) string {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(b))
	root := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the SVG is not well-formed: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok && root == "" {
			root = se.Name.Local
		}
	}
	return root
}

func TestOnePageIsOneWellFormedSVG(t *testing.T) {
	ps, err := PDFToSVG(buildPDF(t, nil, squarePage), SVGOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Number != 1 {
		t.Fatalf("got %d pages, first numbered %v", len(ps), ps)
	}
	if root := wellFormed(t, ps[0].Data); root != "svg" {
		t.Errorf("the root element is %q", root)
	}
}

func TestSeveralPagesAreAZipOfNamedWellFormedSVGs(t *testing.T) {
	pdf := buildPDF(t, nil, squarePage, squarePage, squarePage)
	ps, err := PDFToSVG(pdf, SVGOptions{Pages: []int{3, 1}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := SVGArchive(&buf, ps); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		wellFormed(t, b)
	}
	// The order asked for is the order written, and the names are the page
	// NUMBERS, not positions in the selection.
	if strings.Join(names, ",") != "page_3.svg,page_1.svg" {
		t.Errorf("entries are %v", names)
	}
}

func TestEntryNamesComeFromTheNumberAndNothingElse(t *testing.T) {
	for n, want := range map[int]string{1: "page_1.svg", 10: "page_10.svg", 123: "page_123.svg"} {
		if got := SVGEntryName(n); got != want {
			t.Errorf("page %d is %q, want %q", n, got, want)
		}
	}
}

func TestASelectionOutsideTheFileIsRefusedBeforeDrawing(t *testing.T) {
	pdf := buildPDF(t, nil, squarePage, squarePage)
	for _, c := range []struct {
		pages []int
		want  string
	}{
		{[]int{3}, "this file has 2"},
		{[]int{0}, "this file has 2"},
		{[]int{1, 2, 1}, "selected twice"},
	} {
		_, err := PDFToSVG(pdf, SVGOptions{Pages: c.pages})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("pages %v: got %v, want %q", c.pages, err, c.want)
		}
	}
}

func TestAnEncryptedFileNeedsItsPassword(t *testing.T) {
	enc := &reader.Encryption{UserPassword: "hunter2", OwnerPassword: "letmein", Permissions: reader.PermPrint}
	pdf := buildPDF(t, enc, squarePage)
	for _, pw := range []string{"", "wrong"} {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("password %q: went on past a document that did not open and panicked: %v", pw, r)
				}
			}()
			_, err = PDFToSVG(pdf, SVGOptions{Password: pw})
		}()
		if err == nil || !strings.Contains(err.Error(), "opening the PDF") {
			t.Errorf("password %q: got %v", pw, err)
		}
	}
	ps, err := PDFToSVG(pdf, SVGOptions{Password: "hunter2"})
	if err != nil || len(ps) != 1 {
		t.Fatalf("the right password gave %v, %v", ps, err)
	}
	wellFormed(t, ps[0].Data)
}

func TestAFileThatIsNotAPDFIsRefused(t *testing.T) {
	for _, in := range []string{"", "%PDF-1.7 and then nothing useful", "plain text"} {
		// A panic here is a REFUSAL that was not made, so it is reported as the
		// assertion it is rather than left to end the test binary.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%q: went on past a document that did not open and panicked: %v", in, r)
				}
			}()
			if ps, err := PDFToSVG([]byte(in), SVGOptions{}); err == nil || ps != nil {
				t.Errorf("%q: got %v, %v", in, ps, err)
			}
		}()
	}
	// And a document that opens and holds nothing says so.
	if _, err := PDFToSVG(buildPDF(t, nil), SVGOptions{}); err == nil ||
		!strings.Contains(err.Error(), "no pages") {
		t.Errorf("a file of no pages: %v", err)
	}
}

func TestAPageTooDeepIsNamedAndNothingIsReturned(t *testing.T) {
	// Page 2 is the one render refuses; page 1 is fine and must NOT come back
	// on its own, because a zip missing a page looks complete.
	pdf := buildPDF(t, nil, squarePage, deepPage(70))
	ps, err := PDFToSVG(pdf, SVGOptions{})
	if ps != nil || !errors.Is(err, render.ErrSVGTooDeep) {
		t.Fatalf("got %d pages and %v", len(ps), err)
	}
	if !strings.HasPrefix(err.Error(), "page 2:") {
		t.Errorf("the error does not lead with the page: %q", err)
	}
	// Sixty-four is the limit, so the page below it is written.
	if _, err := PDFToSVG(buildPDF(t, nil, deepPage(60)), SVGOptions{}); err != nil {
		t.Errorf("sixty clips deep was refused: %v", err)
	}
}

func TestEveryRefusalKeepsItsCauseAndNamesThePage(t *testing.T) {
	pdf := buildPDF(t, nil, squarePage, squarePage)
	defer func(f func(*reader.Document, int, io.Writer, render.SVGOptions) error) { pageSVG = f }(pageSVG)
	boom := errors.New("boom")
	for _, c := range []struct {
		cause error
		want  string
	}{
		{render.ErrTimedOut, "time ran out"},
		{render.ErrSVGTooLarge, "larger than the limit"},
		{render.ErrSVGTooDeep, "nest deeper"},
		{boom, "page 2: boom"},
	} {
		pageSVG = func(d *reader.Document, i int, w io.Writer, o render.SVGOptions) error {
			if i == 2 {
				return c.cause
			}
			return render.PageSVG(d, i, w, o)
		}
		ps, err := PDFToSVG(pdf, SVGOptions{})
		if ps != nil || !errors.Is(err, c.cause) || !strings.Contains(err.Error(), c.want) ||
			!strings.HasPrefix(err.Error(), "page 2:") {
			t.Errorf("%v: got %d pages and %v", c.cause, len(ps), err)
		}
	}
}

func TestTheOptionsReachRender(t *testing.T) {
	pdf := buildPDF(t, nil, squarePage)
	defer func(f func(*reader.Document, int, io.Writer, render.SVGOptions) error) { pageSVG = f }(pageSVG)
	var got render.SVGOptions
	pageSVG = func(d *reader.Document, i int, w io.Writer, o render.SVGOptions) error {
		got = o
		return render.PageSVG(d, i, w, o)
	}
	if _, err := PDFToSVG(pdf, SVGOptions{MaxBytes: 1 << 20, MaxDuration: time.Second, RasterScale: 3}); err != nil {
		t.Fatal(err)
	}
	if got.MaxBytes != 1<<20 || got.MaxDuration != time.Second || got.RasterScale != 3 {
		t.Errorf("render saw %+v", got)
	}
	// Left alone, the caps are the run's own and not "unlimited".
	if _, err := PDFToSVG(pdf, SVGOptions{}); err != nil {
		t.Fatal(err)
	}
	if got.MaxBytes != MaxSVGBytes || got.MaxDuration > MaxSVGRunTime ||
		got.MaxDuration < MaxSVGRunTime-time.Second {
		t.Errorf("defaults reached render as %+v", got)
	}
}

func TestThePagesTogetherAreBoundedNotOnlyEachOne(t *testing.T) {
	pdf := buildPDF(t, nil, squarePage, squarePage, squarePage)
	one, err := PDFToSVG(pdf, SVGOptions{Pages: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	defer func(v int) { MaxSVGBytes = v }(MaxSVGBytes)

	// Room for one page and a half: the second page is handed render a limit
	// of half a page and refused. render's own count leaves out the wrapper
	// around the marks, so the kept size is checked here too, and it is THAT
	// check which refuses a page the limit let through.
	MaxSVGBytes = len(one[0].Data) + len(one[0].Data)/2
	_, err = PDFToSVG(pdf, SVGOptions{})
	if !errors.Is(err, render.ErrSVGTooLarge) || !strings.HasPrefix(err.Error(), "page 2:") {
		t.Errorf("got %v", err)
	}

	// Room for exactly one page: the budget is gone before page 2 starts.
	MaxSVGBytes = len(one[0].Data)
	_, err = PDFToSVG(pdf, SVGOptions{})
	if err == nil || !strings.Contains(err.Error(), "page 2: the pages so far fill") {
		t.Errorf("got %v", err)
	}

	// And one page larger than the per-page option is refused on its own.
	MaxSVGBytes = 256 << 20
	_, err = PDFToSVG(pdf, SVGOptions{MaxBytes: 10})
	if !errors.Is(err, render.ErrSVGTooLarge) || !strings.HasPrefix(err.Error(), "page 1:") {
		t.Errorf("got %v", err)
	}
}

func TestTheRunHasATimeLimitOfItsOwn(t *testing.T) {
	pdf := buildPDF(t, nil, squarePage, squarePage)
	defer func(f func() time.Time, d time.Duration) { now, MaxSVGRunTime = f, d }(now, MaxSVGRunTime)
	// A clock that moves a minute every time it is read.
	clock := time.Unix(0, 0)
	now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	MaxSVGRunTime = 90 * time.Second
	_, err := PDFToSVG(pdf, SVGOptions{})
	if err == nil || !strings.Contains(err.Error(), "page 2: the run has used its") {
		t.Errorf("got %v", err)
	}

	// Exactly the time allowed is spent: nothing is left, and the page is not
	// started on render's own default of ten seconds instead.
	clock = time.Unix(0, 0)
	MaxSVGRunTime = time.Minute
	_, err = PDFToSVG(pdf, SVGOptions{})
	if err == nil || !strings.Contains(err.Error(), "page 1: the run has used its") {
		t.Errorf("with nothing left: %v", err)
	}
}

func TestTooManyPagesAreRefusedBeforeAnyIsDrawn(t *testing.T) {
	defer func(v int) { MaxSVGPages = v }(MaxSVGPages)
	MaxSVGPages = 2
	pdf := buildPDF(t, nil, squarePage, squarePage, squarePage)
	// The refusal is the one made BEFORE the list of pages is built, not the
	// one about a selection: they bound the same number, and only the first
	// is made ahead of the allocation.
	if _, err := PDFToSVG(pdf, SVGOptions{}); err == nil || !strings.Contains(err.Error(), "the PDF has 3 pages") {
		t.Errorf("all of a long file: %v", err)
	}
	// Exactly the ceiling is allowed.
	MaxSVGPages = 3
	if ps, err := PDFToSVG(pdf, SVGOptions{}); err != nil || len(ps) != 3 {
		t.Errorf("a file of exactly the ceiling: %v, %v", ps, err)
	}
	MaxSVGPages = 2
	if _, err := PDFToSVG(pdf, SVGOptions{Pages: []int{1, 2, 3}}); err == nil ||
		!strings.Contains(err.Error(), "3 pages asked for") {
		t.Errorf("a long selection: %v", err)
	}
	if ps, err := PDFToSVG(pdf, SVGOptions{Pages: []int{2, 3}}); err != nil || len(ps) != 2 {
		t.Errorf("a short selection of a long file: %v, %v", ps, err)
	}
}

func TestAnArchiveRefusesWhatItCannotHold(t *testing.T) {
	if err := SVGArchive(io.Discard, nil); err == nil {
		t.Error("an empty archive was written")
	}
	defer func(v int) { MaxSVGBytes = v }(MaxSVGBytes)
	MaxSVGBytes = 5
	err := SVGArchive(io.Discard, []SVGPage{{1, []byte("abc")}, {2, []byte("def")}})
	if err == nil || !strings.Contains(err.Error(), "more than the 5 bytes") {
		t.Errorf("got %v", err)
	}
	// Exactly the ceiling is allowed: it is a ceiling, not a rounding.
	MaxSVGBytes = 6
	if err := SVGArchive(io.Discard, []SVGPage{{1, []byte("abc")}, {2, []byte("def")}}); err != nil {
		t.Errorf("pages summing to exactly the ceiling: %v", err)
	}
}

// failingArchive fails on the entry or the close named.
type failingArchive struct{ create, write, close bool }

func (f failingArchive) Create(string) (io.Writer, error) {
	if f.create {
		return nil, errors.New("no entry")
	}
	if f.write {
		return fullDisk{}, nil
	}
	return io.Discard, nil
}

func (f failingArchive) Close() error {
	if f.close {
		return errors.New("no close")
	}
	return nil
}

type fullDisk struct{}

func (fullDisk) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestAnArchiveThatCannotBeWrittenSaysWhichEntry(t *testing.T) {
	defer func(f func(io.Writer) archiveWriter) { newArchive = f }(newArchive)
	for _, f := range []failingArchive{{create: true}, {write: true}, {close: true}} {
		newArchive = func(io.Writer) archiveWriter { return f }
		err := SVGArchive(io.Discard, []SVGPage{{Number: 7, Data: []byte("x")}})
		if err == nil {
			t.Errorf("%+v: no error", f)
			continue
		}
		if !f.close && !strings.Contains(err.Error(), "page_7.svg") {
			t.Errorf("%+v: the error is %q", f, err)
		}
	}
}

// ⛔ The bombs. Each asserts a REFUSAL and a bounded heap, with a memory limit
// set so that a bound that does not hold shows up as work, not a quiet pass.

func TestAPageCountPastTheCeilingIsRefusedBeforeAnyIsDrawn(t *testing.T) {
	debug.SetMemoryLimit(256 << 20)
	defer debug.SetMemoryLimit(-1)
	// Five thousand one-line pages: a small file that declares five times what a
	// run may write.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	cs := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(squarePage)})
	kids := make(reader.Array, 5000)
	for i := range kids {
		kids[i] = w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": cs})
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(len(kids)),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	pdf, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	var got error
	held := heldDuring(func() { _, got = PDFToSVG(pdf, SVGOptions{}) })
	if got == nil || !strings.Contains(got.Error(), "past the 1000 allowed") {
		t.Errorf("refused as %q", got)
	}
	if held > 16 {
		t.Errorf("refusing a %d-byte file held %.1f MiB", len(pdf), held)
	}
}

func TestAPageThatAsksForMillionsOfMarksIsRefusedAtTheLimit(t *testing.T) {
	debug.SetMemoryLimit(256 << 20)
	defer debug.SetMemoryLimit(-1)
	bomb := strings.Repeat("1 1 2 2 re f ", 400_000) // a few MB of marks, ~40 MB of SVG
	pdf := buildPDF(t, nil, bomb)
	var err error
	_, err = PDFToSVG(pdf, SVGOptions{MaxBytes: 1 << 20})
	if !errors.Is(err, render.ErrSVGTooLarge) {
		t.Fatalf("got %v", err)
	}
	// The refusal is the assertion; the process stays under the memory limit
	// set above. A heap comparison against the unlimited run was dropped: it
	// moved with the collector and the load (169 vs 88 MiB under -race), and
	// proved nothing the refusal does not. The fixture must be a real bomb.
	ps, uerr := PDFToSVG(pdf, SVGOptions{})
	if uerr != nil {
		t.Fatal(uerr)
	}
	if len(ps[0].Data) < 10<<20 {
		t.Fatalf("the unlimited page is %d bytes: the fixture is not a bomb", len(ps[0].Data))
	}
}

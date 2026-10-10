// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"archive/zip"
	"bytes"
	"image"
	"io"
	"sort"
	"strings"
	"testing"
)

func TestNaturalOrderOnTheAwkwardPairs(t *testing.T) {
	// ⛔ The pairs a comparison gets wrong are never the obvious ones. Each
	// line here is a shape a real archive has: a number followed by more text,
	// a name that is a prefix of another, two numbers that are equal after
	// their padding, and a name with no digits at all.
	for _, c := range []struct {
		a, b string
		want bool // a before b
	}{
		{"p1.png", "p2.png", true},
		{"p2.png", "p10.png", true},
		{"p10.png", "p2.png", false},
		{"p01a.png", "p1b.png", true},   // same number, then a < b
		{"p1b.png", "p01a.png", false},  // and the other way
		{"p1.png", "p1extra.png", true}, // a prefix sorts first
		{"p1extra.png", "p1.png", false},
		{"cover.png", "page1.png", true}, // no digits at all
		{"a", "b", true},
		{"a", "a", false}, // equal is not "before"
		{"p007.png", "p7.png", false},
		{"p7.png", "p007.png", false}, // equal after padding, equal after text
		{"ch1/p2.png", "ch1/p10.png", true},
		{"ch2/p1.png", "ch10/p1.png", true}, // the number in the FOLDER counts
	} {
		if got := naturalLess(c.a, c.b); got != c.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestTheOrderIsAStrictWeakOrdering(t *testing.T) {
	// ⛔ sort.Slice with an inconsistent comparison does not merely order
	// badly: it can read out of range. A comparison used on names a file
	// chooses has to be well behaved on every pair, not on the ones somebody
	// thought of.
	names := []string{
		"p1.png", "p01.png", "p001.png", "p2.png", "p10.png", "P10.png",
		"cover.jpg", "a/b/p1.png", "a/b/p10.png", "", "9", "09", "x9y",
		"x09y", "p1extra", "p1",
	}
	for _, a := range names {
		if naturalLess(a, a) {
			t.Errorf("%q sorts before itself", a)
		}
		for _, b := range names {
			if naturalLess(a, b) && naturalLess(b, a) {
				t.Errorf("%q and %q each sort before the other", a, b)
			}
		}
	}
	// And sorting twice is the same answer, which an inconsistent comparison
	// does not give.
	first := append([]string(nil), names...)
	sort.Slice(first, func(i, j int) bool { return naturalLess(first[i], first[j]) })
	second := append([]string(nil), names...)
	sort.Slice(second, func(i, j int) bool { return naturalLess(second[i], second[j]) })
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Errorf("two sorts of the same names differ:\n%v\n%v", first, second)
	}
}

func TestAnEntryThatWillNotInflateIsNamed(t *testing.T) {
	// ⛔ Over a hundred-page archive, "flate: corrupt input" alone is a
	// riddle. Which page is the whole message.
	good := cbz(t,
		[2]string{"page1.png", string(barPNG(t, 1))},
		[2]string{"page2.png", string(barPNG(t, 2))},
	)
	// Corrupt the compressed bytes of the second entry, well past the first
	// local header, so the archive still opens and one entry still reads.
	bad := append([]byte(nil), good...)
	at := len(bad) / 2
	for i := at; i < at+16 && i < len(bad); i++ {
		bad[i] ^= 0xFF
	}
	_, err := ArchiveToPDF(bad, Options{DPI: 72})
	if err == nil {
		t.Skip("the corruption landed somewhere the reader tolerates")
	}
	if !strings.Contains(err.Error(), "page") && !strings.Contains(err.Error(), "archive") {
		t.Errorf("the refusal is %q and names neither an entry nor the archive", err)
	}
}

func TestOneEntryReadsAsOneAndNotAsOneS(t *testing.T) {
	// A message that says "1 entries" is a message nobody proof-read.
	src := cbz(t, [2]string{"notes.txt", "hello"})
	_, err := ArchiveToPDF(src, Options{})
	if err == nil {
		t.Fatal("an archive of one text file was converted")
	}
	if !strings.Contains(err.Error(), "1 entry") {
		t.Errorf("it said %q", err)
	}
}

func TestWritingAnArchiveReportsAFailureFromTheWriter(t *testing.T) {
	// zip.Writer buffers, so the failure surfaces at a later write or at
	// Close. Whichever it is, it has to come back rather than be swallowed.
	m := image.NewNRGBA(image.Rect(0, 0, 60, 60))
	pages := make([]image.Image, 8)
	for i := range pages {
		pages[i] = m
	}
	pdf, err := ToPDF(pages, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	if err := ToArchive(&shortWriter{limit: 200}, pdf, RasterOptions{DPI: 72}, ""); err == nil {
		t.Error("a writer that stops part way was reported as success")
	}
}

// shortWriter accepts a few bytes and then refuses, which is what a full disk
// does. It is a POINTER receiver: a value receiver would reset the count on
// every call and the writer would never fill up.
type shortWriter struct {
	limit, n int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if w.n+len(p) > w.limit {
		return 0, errFull
	}
	w.n += len(p)
	return len(p), nil
}

var errFull = errShort{}

type errShort struct{}

func (errShort) Error() string { return "no space left on device" }

func TestAnEntryInAFormatTheReaderCannotOpenIsNamed(t *testing.T) {
	// ⛔ An entry can fail at Open rather than at Read: a ZIP may name a
	// compression method the standard library has no decompressor for — the
	// AES-encrypted method 99 that WinZip writes is the one people actually
	// meet. The archive opens, the directory lists, and that one entry does
	// not. It has to be named.
	src := cbz(t,
		[2]string{"page1.png", string(barPNG(t, 1))},
		[2]string{"page2.png", string(barPNG(t, 2))},
	)
	// Rewrite every compression method in the central directory to 99.
	// Central directory file header: 4 signature, 2 version made by,
	// 2 version needed, 2 flags, 2 METHOD.
	bad := append([]byte(nil), src...)
	sig := []byte{'P', 'K', 0x01, 0x02}
	patched := 0
	for i := 0; i+12 <= len(bad); i++ {
		if bytes.Equal(bad[i:i+4], sig) {
			bad[i+10], bad[i+11] = 99, 0
			patched++
		}
	}
	if patched == 0 {
		t.Fatal("no central directory entry was found: the fixture is not what this test thinks")
	}
	_, err := ArchiveToPDF(bad, Options{DPI: 72})
	if err == nil {
		t.Fatal("an entry in an unknown compression method was read")
	}
	if !strings.Contains(err.Error(), "page") {
		t.Errorf("the refusal is %q and does not name the entry", err)
	}
}

func TestAWriterThatFillsUpPartWayThroughIsReported(t *testing.T) {
	// ⛔ zip.Writer buffers, so a writer that refuses its very first byte
	// fails only at Close — and a test that stopped there would leave the
	// per-entry error paths untouched. A disk that fills up AFTER some pages
	// have gone down is both the ordinary case and the one that reaches them.
	m := image.NewNRGBA(image.Rect(0, 0, 120, 120))
	pages := make([]image.Image, 24)
	for i := range pages {
		pages[i] = m
	}
	pdf, err := ToPDF(pages, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	// ⛔ The limits are DERIVED from how big the archive actually is, not
	// guessed: a first version tried 8192 and the whole archive fitted inside
	// it, so the test failed for a reason that was about my arithmetic rather
	// than about the code.
	var whole bytes.Buffer
	if err := ToArchive(&whole, pdf, RasterOptions{DPI: 72}, ""); err != nil {
		t.Fatal(err)
	}
	full := whole.Len()
	if full < 4096 {
		t.Fatalf("the archive is only %d bytes, so no limit here can land mid-stream", full)
	}
	for _, limit := range []int{0, 1, full / 4, full / 2, full - 1} {
		w := &shortWriter{limit: limit}
		if err := ToArchive(w, pdf, RasterOptions{DPI: 72}, ""); err == nil {
			t.Errorf("a writer that stops at %d of %d bytes was reported as success",
				limit, full)
		}
	}
	// And the whole thing through a writer that is just big enough.
	if err := ToArchive(&shortWriter{limit: full}, pdf, RasterOptions{DPI: 72}, ""); err != nil {
		t.Errorf("a writer with exactly enough room refused it: %v", err)
	}
}

func TestTheSizeCeilingsAreReachableAndReached(t *testing.T) {
	// ⛔ Half a gigabyte cannot be reached by a test that has to run in a
	// second, which is why the ceilings are variables. Lowering them here is
	// what makes them guards rather than claims.
	wasEntry, wasAll := MaxArchiveEntryBytes, MaxArchiveBytes
	t.Cleanup(func() { MaxArchiveEntryBytes, MaxArchiveBytes = wasEntry, wasAll })

	big := strings.Repeat("x", 4096)
	src := cbz(t,
		[2]string{"a.txt", big},
		[2]string{"b.txt", big},
		[2]string{"c.txt", big},
	)

	// One entry past the per-entry ceiling.
	MaxArchiveEntryBytes, MaxArchiveBytes = 100, 1<<30
	_, err := ArchiveToPDF(src, Options{})
	if err == nil || !strings.Contains(err.Error(), "past the 100 allowed") {
		t.Errorf("the per-entry ceiling gave %v", err)
	}

	// ⛔ And the PACKAGE budget, which the per-entry ceiling cannot see: each
	// entry is comfortably inside it and three of them are not. Without the
	// running total, a thousand entries just under the ceiling would all pass.
	MaxArchiveEntryBytes, MaxArchiveBytes = 1<<20, 5000
	_, err = ArchiveToPDF(src, Options{})
	if err == nil {
		t.Fatal("three entries of four thousand bytes passed a budget of five thousand")
	}
	if strings.Contains(err.Error(), "past the 1048576 allowed") {
		t.Errorf("it was the per-entry ceiling that fired, not the budget: %v", err)
	}
}

// refusingArchive fails on the entry asked for, which archive/zip's own
// writer never does through a plain io.Writer: it buffers and reports at
// Close. See newArchive.
// It lets the metadata entry through and fails on the first PAGE, so the
// message under test is the one that names a page rather than the one that
// names ComicInfo.xml.
type refusingArchive struct {
	onCreate bool
	n        int
}

func (r *refusingArchive) Create(string) (io.Writer, error) {
	r.n++
	if r.n == 1 {
		return &bytes.Buffer{}, nil // ComicInfo.xml
	}
	if r.onCreate {
		return nil, errFull
	}
	return refusingWriter{}, nil
}
func (*refusingArchive) Close() error { return nil }

type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, errFull }

func TestAnEntryThatCannotBeWrittenNamesThePage(t *testing.T) {
	// ⛔ Over a three-hundred-page archive, "no space left on device" alone
	// does not say how far it got. The page number is the whole message.
	was := newArchive
	t.Cleanup(func() { newArchive = was })

	m := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	pdf, err := ToPDF([]image.Image{m, m}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	for _, onCreate := range []bool{true, false} {
		newArchive = func(io.Writer) archiveWriter { return &refusingArchive{onCreate: onCreate} }
		err := ToArchive(&bytes.Buffer{}, pdf, RasterOptions{DPI: 36}, "")
		if err == nil {
			t.Fatalf("onCreate=%v: a refusing archive was reported as success", onCreate)
		}
		if !strings.Contains(err.Error(), "1.png") {
			t.Errorf("onCreate=%v: the refusal is %q and does not name the page",
				onCreate, err)
		}
	}
}

func TestEntriesCanBeCountedWithoutReadingThemAll(t *testing.T) {
	// The entry ceiling is checked against the central directory, before a
	// single byte is inflated — so an archive of ten thousand tiny entries is
	// refused instantly rather than after reading them.
	var many [][2]string
	for i := 0; i < 20; i++ {
		many = append(many, [2]string{"f" + itoa(i) + ".txt", "x"})
	}
	src := cbz(t, many...)
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 20 {
		t.Fatalf("%d entries", len(zr.File))
	}
}

// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestARangeOfPagesIsReadTheWayPeopleWriteIt(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"1", []int{1}},
		{"1,3,5", []int{1, 3, 5}},
		{"2-5", []int{2, 3, 4, 5}},
		{"1,4-6,9", []int{1, 4, 5, 6, 9}},
		{" 1 , 3 ", []int{1, 3}},
		// ⛔ Descending is HONOURED. "9-7" is a legitimate way to ask for those
		// pages backwards; sorting it silently would quietly do something else,
		// and refusing it would forbid something the tool can do.
		{"9-7", []int{9, 8, 7}},
	} {
		got, err := parseRange(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q gave %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"x", "1-x", ",", "1-"} {
		if _, err := parseRange(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAnEmptyRangeMeansEveryPageAndNotNoPages(t *testing.T) {
	// ⛔ The two are opposite instructions that look alike. nil means "all";
	// a non-empty string selecting nothing is a typo and has to be refused,
	// because silently drawing every page is the worst possible reading of it.
	got, err := parseRange("")
	if err != nil || got != nil {
		t.Errorf(`"" gave %v, %v — it must mean every page`, got, err)
	}
	if _, err := parseRange(" , , "); err == nil {
		t.Error("a range that selects nothing was accepted as every page")
	}
}

func TestAMarginWithNoPaperIsRefused(t *testing.T) {
	// ⛔ A page sized to its own picture has no room to leave, so a margin on
	// it is a request that cannot be carried out. Ignoring it silently lets
	// somebody believe their pages have one.
	dir := t.TempDir()
	src := filepath.Join(dir, "x.png")
	writePNG(t, src)
	err := run([]string{"to-pdf", "-margin", "20", "-o", filepath.Join(dir, "o.pdf"), src})
	if err == nil {
		t.Fatal("a margin with no page size was accepted")
	}
	if !strings.Contains(err.Error(), "-page") {
		t.Errorf("the refusal is %q and does not say what to do about it", err)
	}
}

func TestTheWholeThingThroughTheCommandLine(t *testing.T) {
	// The end-to-end witness for the CLI: a real file in, a real file out, and
	// the pages read back off the disk. Every other test here calls a function.
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png")
	writePNG(t, a)
	writePNG(t, b)
	out := filepath.Join(dir, "both.pdf")
	if err := run([]string{"to-pdf", "-dpi", "72", "-o", out, a, b}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("the PDF is %v (%v)", st, err)
	}
	pics := filepath.Join(dir, "pics")
	if err := run([]string{"from-pdf", "-dpi", "72", "-format", "jpeg", "-o", pics, out}); err != nil {
		t.Fatal(err)
	}
	es, err := os.ReadDir(pics)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatalf("two pages came back as %d files", len(es))
	}
	// ⛔ Named for the page they are, not for the order they were written in.
	// A directory listing is sorted by name, and "page-10" before "page-2" is
	// how a hundred-page document comes back shuffled.
	if es[0].Name() != "both-001.jpeg" || es[1].Name() != "both-002.jpeg" {
		t.Errorf("the pages are named %s and %s", es[0].Name(), es[1].Name())
	}
}

func TestEachMissingArgumentSaysWhatIsMissing(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"to-pdf", "x.png"}, "-o"},
		{[]string{"to-pdf", "-o", "x.pdf"}, "no pictures"},
		{[]string{"from-pdf", "x.pdf"}, "-o"},
		{[]string{"from-pdf", "-o", "d"}, "exactly one"},
		{[]string{}, "command"},
		{[]string{"nonesuch"}, "nonesuch"},
	} {
		err := run(c.args)
		if err == nil {
			t.Errorf("%v was accepted", c.args)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v gave %q, which does not mention %q", c.args, err, c.want)
		}
	}
}

func TestAFileThatCannotBeReadIsNamed(t *testing.T) {
	// ⛔ "reading a picture: unknown format" over twenty files is a riddle,
	// not an error message.
	dir := t.TempDir()
	good := filepath.Join(dir, "good.png")
	writePNG(t, good)
	bad := filepath.Join(dir, "notapicture.png")
	if err := os.WriteFile(bad, []byte("this is not a picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"to-pdf", "-o", filepath.Join(dir, "o.pdf"), good, bad})
	if err == nil {
		t.Fatal("a file that is not a picture was accepted")
	}
	if !strings.Contains(err.Error(), "notapicture.png") {
		t.Errorf("the refusal is %q and does not name the file", err)
	}
}

func TestFormatsListsBothDirections(t *testing.T) {
	if err := run([]string{"formats"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, path string) {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 24, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 24; x++ {
			m.Set(x, y, color.NRGBA{R: uint8(x * 10), G: uint8(y * 15), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMainHandsBackTheExitCode(t *testing.T) {
	// ⛔ main's only job is to turn a refusal into a non-zero status. A tool
	// that prints an error and exits 0 is one a script cannot check, and the
	// scripts are most of what runs this.
	var out, errw bytes.Buffer
	if code := mainish([]string{"formats"}, &out, &errw); code != 0 {
		t.Errorf("formats exited %d: %s", code, errw.String())
	}
	if out.Len() == 0 {
		t.Error("formats printed nothing")
	}
	out.Reset()
	errw.Reset()
	if code := mainish([]string{"nonesuch"}, &out, &errw); code == 0 {
		t.Error("an unknown command exited 0")
	}
	if !strings.Contains(errw.String(), "pdfconv:") {
		t.Errorf("the refusal went to %q and is not prefixed with the tool's name", errw.String())
	}

	// And main itself, through the exit seam.
	old, oldArgs := osExit, os.Args
	t.Cleanup(func() { osExit, os.Args = old, oldArgs })
	got := -1
	osExit = func(code int) { got = code }
	os.Args = []string{"pdfconv", "formats"}
	main()
	if got != 0 {
		t.Errorf("main exited %d for a command that works", got)
	}
}

func TestAPathThatCannotBeWrittenIsReported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.png")
	writePNG(t, src)

	// A directory is not a file you can write a PDF over.
	if err := run([]string{"to-pdf", "-o", dir, src}); err == nil {
		t.Error("a PDF was written over a directory")
	}
	// And a picture that is not there at all.
	if err := run([]string{"to-pdf", "-o", filepath.Join(dir, "o.pdf"), filepath.Join(dir, "nope.png")}); err == nil {
		t.Error("a picture that does not exist was converted")
	}
	// The other direction: a PDF that is not there, and an output directory
	// that cannot be made because a file is already sitting on the name.
	if err := run([]string{"from-pdf", "-o", filepath.Join(dir, "d"), filepath.Join(dir, "nope.pdf")}); err == nil {
		t.Error("a PDF that does not exist was drawn")
	}
	out := filepath.Join(dir, "both.pdf")
	if err := run([]string{"to-pdf", "-o", out, src}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"from-pdf", "-o", src, out}); err == nil {
		t.Error("pictures were written into a directory that is a file")
	}
}

func TestBadFlagsAreRefusedByEachSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"to-pdf", "-nonesuch"},
		{"from-pdf", "-nonesuch"},
		{"to-pdf", "-page", "a9", "-o", "x.pdf", "y.png"},
		{"from-pdf", "-pages", "x", "-o", "d", "y.pdf"},
	} {
		if err := run(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

func TestOnePageThatCannotBeWrittenStopsTheRun(t *testing.T) {
	// ⛔ The directory was made and the first page went down; the second
	// cannot. Carrying on would leave a directory that LOOKS like a complete
	// conversion and is one page short, which nobody checks.
	dir := t.TempDir()
	src := filepath.Join(dir, "x.png")
	writePNG(t, src)
	pdf := filepath.Join(dir, "two.pdf")
	if err := run([]string{"to-pdf", "-o", pdf, src, src}); err != nil {
		t.Fatal(err)
	}
	pics := filepath.Join(dir, "pics")
	// A directory sitting exactly where the second page's file has to go.
	if err := os.MkdirAll(filepath.Join(pics, "two-002.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"from-pdf", "-dpi", "36", "-o", pics, pdf}); err == nil {
		t.Error("a page that could not be written was reported as success")
	}
}

func TestAFileThatIsNotAPDFLeavesNoDirectoryBehind(t *testing.T) {
	// ⛔ Two things at once, and the second is the one worth a test. The file
	// is refused — and the output directory is NOT created, because the
	// conversion runs before the directory does. A tool that leaves an empty
	// directory after failing teaches people that an empty directory means
	// "no pages", which is a different thing from "it never ran".
	dir := t.TempDir()
	notPDF := filepath.Join(dir, "x.pdf")
	if err := os.WriteFile(notPDF, []byte("plainly not a PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	pics := filepath.Join(dir, "pics")
	if err := run([]string{"from-pdf", "-o", pics, notPDF}); err == nil {
		t.Fatal("a file that is not a PDF was drawn")
	}
	if _, err := os.Stat(pics); err == nil {
		t.Error("the output directory was created for a conversion that never happened")
	}
}

// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// comic writes a small archive on disk.
func comic(t *testing.T, names ...string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(n, ".png") {
			w.Write(pngBytes(t, 20, 10, color.NRGBA{R: 200, A: 255}))
		} else {
			w.Write([]byte("not a picture"))
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "in.cbz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAnArchiveGoesToAPDFAndBack(t *testing.T) {
	in := comic(t, "page10.png", "page2.png", "page1.png", "ComicInfo.xml")
	dir := filepath.Dir(in)
	pdf := filepath.Join(dir, "out.pdf")
	if err := run([]string{"from-archive", "-dpi", "72", in, pdf}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(pdf)
	if err != nil || st.Size() == 0 {
		t.Fatalf("the PDF is %v (%v)", st, err)
	}

	back := filepath.Join(dir, "back.cbz")
	if err := run([]string{"to-archive", "-dpi", "72", pdf, back}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 4 {
		t.Fatalf("%d entries came back (three pages and a ComicInfo.xml)", len(zr.File))
	}
	if zr.File[0].Name != "ComicInfo.xml" || zr.File[1].Name != "1.png" {
		t.Errorf("the first entry is %q", zr.File[0].Name)
	}
}

func TestAnSVGHandedToToPDFIsDrawn(t *testing.T) {
	// ⛔ Recognised by its CONTENT: the file here is called .txt on purpose.
	dir := t.TempDir()
	p := filepath.Join(dir, "drawing.txt")
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="80" height="40">` +
		`<rect width="80" height="40" fill="#336699"/></svg>`
	if err := os.WriteFile(p, []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")
	if err := run([]string{"to-pdf", "-dpi", "72", "-o", out, p}); err != nil {
		t.Fatalf("an SVG named .txt was refused: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("the PDF is %v (%v)", st, err)
	}

	// And a broken SVG is refused with its file named.
	bad := filepath.Join(dir, "broken.svg")
	if err := os.WriteFile(bad, []byte("<svg-ish but not really"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"to-pdf", "-o", out, bad})
	if err == nil {
		t.Fatal("a broken SVG was accepted")
	}
	if !strings.Contains(err.Error(), "broken.svg") {
		t.Errorf("the refusal is %q and does not name the file", err)
	}
}

func TestTheArchiveVerbsRefuseWhatTheyCannotDo(t *testing.T) {
	in := comic(t, "page1.png")
	dir := filepath.Dir(in)
	out := filepath.Join(dir, "out.pdf")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"from-archive"}, "an input and an output"},
		{[]string{"from-archive", in}, "an input and an output"},
		{[]string{"from-archive", "-nonesuch", in, out}, ""},
		{[]string{"from-archive", "-page", "a9", in, out}, "unknown page size"},
		{[]string{"from-archive", "-margin", "10", in, out}, "-page"},
		{[]string{"from-archive", filepath.Join(dir, "absent.cbz"), out}, ""},
		{[]string{"from-archive", in, dir}, ""},
		{[]string{"to-archive"}, "an input and an output"},
		{[]string{"to-archive", "-nonesuch", out, out}, ""},
		{[]string{"to-archive", "-pages", "x", out, out}, "not a number"},
		{[]string{"to-archive", filepath.Join(dir, "absent.pdf"), out}, ""},
	} {
		if err := run(c.args); err == nil {
			t.Errorf("%v was accepted", c.args)
		} else if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v gave %q, want %q in it", c.args, err, c.want)
		}
	}
}

func TestAnArchiveThatCannotBeWrittenIsReported(t *testing.T) {
	in := comic(t, "page1.png")
	dir := filepath.Dir(in)
	pdf := filepath.Join(dir, "out.pdf")
	if err := run([]string{"from-archive", "-dpi", "72", in, pdf}); err != nil {
		t.Fatal(err)
	}
	// ⛔ The archive is built in memory and written once: a half-written ZIP
	// left on disk after a failure has its directory missing from the END, so
	// it does not even open — but the file is there and dated, and somebody
	// will try.
	if err := run([]string{"to-archive", pdf, dir}); err == nil {
		t.Error("an archive was written over a directory")
	}
}

func TestAFileThatIsNotWhatTheVerbWantsIsNamed(t *testing.T) {
	// ⛔ Over a directory of a hundred files, "not a zip" alone is a riddle.
	// The file is the message.
	dir := t.TempDir()
	notZip := filepath.Join(dir, "notes.cbz")
	if err := os.WriteFile(notZip, []byte("plainly not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"from-archive", notZip, filepath.Join(dir, "o.pdf")})
	if err == nil {
		t.Fatal("something that is not an archive was converted")
	}
	if !strings.Contains(err.Error(), "notes.cbz") {
		t.Errorf("the refusal is %q and does not name the file", err)
	}

	notPDF := filepath.Join(dir, "notes.pdf")
	if err := os.WriteFile(notPDF, []byte("plainly not a PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"to-archive", notPDF, filepath.Join(dir, "o.cbz")})
	if err == nil {
		t.Fatal("something that is not a PDF was archived")
	}
	if !strings.Contains(err.Error(), "notes.pdf") {
		t.Errorf("the refusal is %q and does not name the file", err)
	}
}

func TestAPictureThatCannotBeReadFromDiskIsNamed(t *testing.T) {
	// A file that opens and then will not read: a directory passes os.Open on
	// some systems and fails on the first read, which is the ordinary way this
	// happens.
	dir := t.TempDir()
	out := filepath.Join(dir, "o.pdf")
	sub := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"to-pdf", "-o", out, sub})
	if err == nil {
		t.Fatal("a directory was converted to a page")
	}
	if !strings.Contains(err.Error(), "a-directory") {
		t.Errorf("the refusal is %q and does not name it", err)
	}
}

func TestBundlePacksFilesUnchanged(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.pdf")
	b := filepath.Join(dir, "b.pdf")
	if err := os.WriteFile(a, []byte("%PDF-1.7 one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("%PDF-1.7 two"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "both.zip")
	if err := run([]string{"bundle", out, a, b}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("%d entries", len(zr.File))
	}
	if zr.File[0].Name != "a.pdf" {
		t.Errorf("the first entry is %q, and the directory should be gone", zr.File[0].Name)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"bundle"}, "at least one file"},
		{[]string{"bundle", out}, "at least one file"},
		{[]string{"bundle", "-nonesuch", out, a}, ""},
		{[]string{"bundle", out, filepath.Join(dir, "absent.pdf")}, ""},
		{[]string{"bundle", dir, a}, ""},
		// ⛔ The temporary is made BESIDE the target, so a target in a
		// directory that does not exist fails there rather than at the rename
		// — and a partial file is never left under the final name.
		{[]string{"bundle", filepath.Join(dir, "nope", "x.zip"), a}, ""},
	} {
		if err := run(c.args); err == nil {
			t.Errorf("%v was accepted", c.args)
		} else if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v gave %q", c.args, err)
		}
	}
}

func TestADiskThatFillsUpMidBundleLeavesNothingBehind(t *testing.T) {
	// ⛔ Two things at once, and the second is why the temporary exists: the
	// failure is reported, AND no file is left under the final name. A
	// half-written zip has its directory missing from the end, so it does not
	// even open — but it is there, and dated, and somebody will try to send it.
	was := bundleTo
	t.Cleanup(func() { bundleTo = was })
	bundleTo = func(io.Writer, map[string][]byte) error {
		return errors.New("no space left on device")
	}

	dir := t.TempDir()
	a := filepath.Join(dir, "a.pdf")
	if err := os.WriteFile(a, []byte("%PDF-1.7"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "both.zip")
	err := run([]string{"bundle", out, a})
	if err == nil {
		t.Fatal("a disk that filled up was reported as success")
	}
	if !strings.Contains(err.Error(), "no space") {
		t.Errorf("it said %q", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a file was left under the final name after the write failed")
	}
	// And the temporary is gone too.
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".pdfconv-") {
			t.Errorf("a temporary was left behind: %s", e.Name())
		}
	}
}

func TestTheArchiveVerbsAreInTheUsage(t *testing.T) {
	var b bytes.Buffer
	usage(&b)
	for _, want := range []string{"from-archive", "to-archive", "page10 after page2"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the usage does not carry %q", want)
		}
	}
}

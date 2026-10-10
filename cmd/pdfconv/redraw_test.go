// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/convert"
)

// onePagePDF writes a one-page PDF on disk and returns its path.
func onePagePDF(t *testing.T) string {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			m.SetNRGBA(x, y, color.NRGBA{R: 200, G: 120, B: 60, A: 255})
		}
	}
	pdf, err := convert.ToPDF([]image.Image{m}, convert.Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(p, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRedrawWritesAFileAndSaysWhatItDid(t *testing.T) {
	in := onePagePDF(t)
	out := filepath.Join(filepath.Dir(in), "out.pdf")
	var o, e bytes.Buffer
	if err := run([]string{"redraw", "-greyscale", "-dpi", "60", in, out}); err != nil {
		t.Fatalf("%v", err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("the file is %v (%v)", st, err)
	}
	// And through runTo, so the message can be read.
	o.Reset()
	e.Reset()
	if err := runTo([]string{"redraw", "-invert", in, out}, &o, &e); err != nil {
		t.Fatal(err)
	}
	// ⛔ The message has to say the text is gone. Somebody who meant to rotate
	// a document and typed this gets a file with no text in it, and will not
	// find out until they try to search it.
	if !strings.Contains(o.String(), "rasterised") {
		t.Errorf("it said %q", o.String())
	}
}

func TestRedrawWithNoChangeWarnsBeforeDoingIt(t *testing.T) {
	// ⛔ Said BEFORE the work, not after. A redraw that changes nothing is
	// `rasterize-pdf`, and somebody who wanted pdfops has just lost their text
	// for no gain at all.
	in := onePagePDF(t)
	out := filepath.Join(filepath.Dir(in), "out.pdf")
	var o, e bytes.Buffer
	if err := runTo([]string{"redraw", in, out}, &o, &e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.String(), "WITHOUT TEXT") {
		t.Errorf("no warning: %q", e.String())
	}
	if !strings.Contains(e.String(), "pdfops") {
		t.Errorf("the warning does not say where to go instead: %q", e.String())
	}
	// And with a change asked for, it says nothing.
	e.Reset()
	if err := runTo([]string{"redraw", "-greyscale", in, out}, &o, &e); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.String(), "WITHOUT TEXT") {
		t.Errorf("it warned about a redraw that was asked for: %q", e.String())
	}
}

func TestEveryColourSpellingIsReadAndTheRestRefused(t *testing.T) {
	// ⛔ A typo must not fall back to black: a wrong background paints every
	// page black under the content, and on a page whose content is opaque that
	// is invisible until the one page that is not.
	for in, want := range map[string]color.RGBA{
		"#fff":      {R: 255, G: 255, B: 255, A: 255},
		"#ffeecc":   {R: 255, G: 238, B: 204, A: 255},
		"ffeecc":    {R: 255, G: 238, B: 204, A: 255},
		"#ffeecc80": {R: 255, G: 238, B: 204, A: 128},
		" #000 ":    {R: 0, G: 0, B: 0, A: 255},
	} {
		got, err := parseColour(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q gave %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "#ff", "#fffff", "red", "#gggggg", "#12345"} {
		if _, err := parseColour(bad); err == nil {
			t.Errorf("%q was accepted as a colour", bad)
		}
	}
}

func TestTheBackgroundFlagReachesTheRedraw(t *testing.T) {
	in := onePagePDF(t)
	out := filepath.Join(filepath.Dir(in), "out.pdf")
	if err := run([]string{"redraw", "-background", "#ffeecc", in, out}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"redraw", "-background", "notacolour", in, out}); err == nil {
		t.Error("a bad colour was accepted")
	}
}

func TestTheScannerFlagsReachTheEffect(t *testing.T) {
	in := onePagePDF(t)
	dir := filepath.Dir(in)
	a := filepath.Join(dir, "a.pdf")
	b := filepath.Join(dir, "b.pdf")
	if err := run([]string{"redraw", "-scanner", "-seed", "1", "-dpi", "40", in, a}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"redraw", "-scanner", "-seed", "2", "-dpi", "40", in, b}); err != nil {
		t.Fatal(err)
	}
	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if bytes.Equal(ba, bb) {
		t.Error("two different seeds produced the same file")
	}
}

func TestRedrawRefusesWhatItCannotDo(t *testing.T) {
	in := onePagePDF(t)
	out := filepath.Join(filepath.Dir(in), "out.pdf")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"redraw"}, "an input and an output"},
		{[]string{"redraw", in}, "an input and an output"},
		{[]string{"redraw", "-nonesuch", in, out}, ""},
		{[]string{"redraw", "-pages", "x", in, out}, "not a number"},
		{[]string{"redraw", "-brightness", "5", in, out}, "brightness"},
		{[]string{"redraw", filepath.Join(filepath.Dir(in), "absent.pdf"), out}, ""},
		{[]string{"redraw", in, filepath.Dir(in)}, ""},
	} {
		err := run(c.args)
		if err == nil {
			t.Errorf("%v was accepted", c.args)
			continue
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v gave %q, want %q in it", c.args, err, c.want)
		}
	}
}

func TestRedrawIsInTheUsage(t *testing.T) {
	var b bytes.Buffer
	usage(&b)
	for _, want := range []string{"redraw", "RASTERISES", "pdfops"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the usage does not carry %q", want)
		}
	}
}

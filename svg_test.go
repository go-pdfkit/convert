// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

const anSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100" viewBox="0 0 200 100">
  <rect width="200" height="100" fill="#ffffff"/>
  <rect x="0" y="0" width="100" height="100" fill="#ff0000"/>
</svg>`

func TestAnSVGIsDrawnAndLandsOnAPage(t *testing.T) {
	pdf, err := SVGToPDF([]byte(anSVG), Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	b := mediaBoxes(t, pdf)
	if len(b) != 1 {
		t.Fatalf("%d pages", len(b))
	}
	// At 72 dpi one SVG user unit is one point, so the page is the document's
	// own size. A page of some other size means the scale was lost.
	if !near(b[0][0], 200, 2) || !near(b[0][1], 100, 2) {
		t.Errorf("a 200x100 drawing came out on a %.0fx%.0f page", b[0][0], b[0][1])
	}

	// ⛔ And the ink actually landed. A drawing that produced a blank page of
	// the right size would pass every check above.
	ps, err := FromPDF(pdf, RasterOptions{DPI: 36, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := png.Decode(bytes.NewReader(ps[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	bb := m.Bounds()
	r, g, _, _ := m.At(bb.Min.X+bb.Dx()/4, bb.Min.Y+bb.Dy()/2).RGBA()
	if r>>8 < 200 || g>>8 > 80 {
		t.Errorf("the left half is rgb(%d,%d,…) and it was painted red", r>>8, g>>8)
	}
}

func TestTheDPIDecidesHowLargeADrawingIs(t *testing.T) {
	// The same document at twice the resolution is the same PAGE: the drawing
	// is sharper, not bigger. Getting this backwards doubles every page.
	a, err := SVGToPDF([]byte(anSVG), Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	b, err := SVGToPDF([]byte(anSVG), Options{DPI: 144})
	if err != nil {
		t.Fatal(err)
	}
	pa, pb := mediaBoxes(t, a), mediaBoxes(t, b)
	if !near(pa[0][0], pb[0][0], 2) {
		t.Errorf("72 dpi gave a %.0f-point page and 144 dpi gave %.0f", pa[0][0], pb[0][0])
	}
	if len(b) <= len(a) {
		t.Errorf("twice the resolution produced %d bytes against %d: it drew no more pixels",
			len(b), len(a))
	}
}

func TestAnSVGIsRecognisedByItsContent(t *testing.T) {
	// ⛔ Not by its name, like every other format here. Inkscape writes an XML
	// declaration, a doctype and a comment before the root element, and a
	// sniffer that wanted "<svg" in the first four bytes would refuse most of
	// what is out there.
	for _, s := range []string{
		anSVG,
		"\xEF\xBB\xBF" + anSVG,
		"<?xml version=\"1.0\"?>\n<!-- made by something -->\n" + anSVG,
		"\n\n   " + anSVG,
		"<SVG xmlns=\"http://www.w3.org/2000/svg\"></SVG>",
	} {
		if !LooksLikeSVG([]byte(s)) {
			t.Errorf("not recognised: %.40q", s)
		}
	}
	for _, s := range []string{
		"", "hello", "\x89PNG\r\n", "{\"svg\":true}",
		"<html><body>the word svg appears here</body></html>",
	} {
		if LooksLikeSVG([]byte(s)) {
			t.Errorf("wrongly recognised: %.40q", s)
		}
	}
}

func TestTheWordSVGFarIntoAFileIsNotAnSVG(t *testing.T) {
	// The sniff looks at the head only, so a megabyte of something else that
	// mentions <svg> at the end is not an SVG.
	s := "<html>" + strings.Repeat("x", 4000) + "<svg/></html>"
	if LooksLikeSVG([]byte(s)) {
		t.Error("a mention four thousand bytes in was taken for the root element")
	}
}

func TestSomethingThatIsNotAnSVGIsRefused(t *testing.T) {
	if _, err := RasterizeSVG([]byte("<nope/>"), Options{}); err == nil {
		t.Error("a document with no svg root was drawn")
	}
	if _, err := SVGToPDF(make([]byte, maxSourceBytes+1), Options{}); err == nil {
		t.Error("a drawing past the source ceiling was read")
	}
}

func TestTheSurfaceCeilingIsPassedThrough(t *testing.T) {
	// ⛔ An SVG is the kind of thing people accept from strangers, and its own
	// width and height decide an allocation: a hundred bytes declaring
	// width="40000" allocated 6.1 GiB before go-gfx bounded it. A caller who
	// sets MaxPixels here means it to apply to the whole chain, not to
	// everything except the part that reads a stranger's file.
	// ⛔ The surface is 2000x2000 — four megapixels, comfortably UNDER
	// go-gfx's own default of forty million, and far over the thousand passed
	// here. A first version used 40000x40000, which the default refuses too,
	// so the test passed whether or not the ceiling was passed through and the
	// mutation that stopped passing it survived.
	big := `<svg xmlns="http://www.w3.org/2000/svg" width="2000" height="2000"><rect width="9" height="9"/></svg>`
	if _, err := RasterizeSVG([]byte(big), Options{DPI: 72, MaxPixels: 1000}); err == nil {
		t.Fatal("a four-megapixel surface was drawn under a ceiling of a thousand")
	}
	// And the same document passes when nothing is asked for, which is what
	// says the refusal above came from the number rather than from the shape.
	if _, err := RasterizeSVG([]byte(big), Options{DPI: 72}); err != nil {
		t.Errorf("four megapixels was refused under the default ceiling: %v", err)
	}
	// And the default still allows an ordinary drawing.
	if _, err := RasterizeSVG([]byte(anSVG), Options{DPI: 72}); err != nil {
		t.Errorf("an ordinary drawing was refused: %v", err)
	}
}

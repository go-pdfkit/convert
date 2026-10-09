// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	"github.com/go-pdfkit/pdfkit"
)

// swatch is a picture whose four quadrants are four different colours, so that
// a round trip can be checked for ORIENTATION as well as for colour. A flat
// fill survives being drawn upside down.
func swatch(w, h int) image.Image {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	q := []color.NRGBA{
		{R: 220, G: 30, B: 30, A: 255},  // top left
		{R: 30, G: 120, B: 220, A: 255}, // top right
		{R: 240, G: 200, B: 40, A: 255}, // bottom left
		{R: 30, G: 160, B: 70, A: 255},  // bottom right
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := 0
			if x >= w/2 {
				i++
			}
			if y >= h/2 {
				i += 2
			}
			m.Set(x, y, q[i])
		}
	}
	return m
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestAPictureComesBackThroughAPDF(t *testing.T) {
	// ⛔ The witness for the whole package. Everything else checks a number
	// this code computed; this one puts a picture in one end and looks at what
	// a renderer draws out of the other. A page can be the right size, carry
	// the right XObject and draw nothing.
	src := swatch(200, 120)
	pdf, err := ToPDF([]image.Image{src}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	pages, err := FromPDF(pdf, RasterOptions{DPI: 72, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("one picture became %d pages", len(pages))
	}
	got, err := png.Decode(bytes.NewReader(pages[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	b := got.Bounds()
	if b.Dx() != 200 || b.Dy() != 120 {
		t.Errorf("a 200x120 picture at 72 dpi came back %dx%d", b.Dx(), b.Dy())
	}
	// Sample inside each quadrant rather than at an edge: the renderer
	// antialiases the boundary, and a test that sampled it would be about
	// antialiasing instead of about the picture.
	for _, c := range []struct {
		name    string
		x, y    int
		r, g, b uint8
	}{
		{"haut gauche", 50, 30, 220, 30, 30},
		{"haut droite", 150, 30, 30, 120, 220},
		{"bas gauche", 50, 90, 240, 200, 40},
		{"bas droite", 150, 90, 30, 160, 70},
	} {
		r, g, bb, _ := got.At(c.x, c.y).RGBA()
		if abs8(r>>8, c.r) > 6 || abs8(g>>8, c.g) > 6 || abs8(bb>>8, c.b) > 6 {
			t.Errorf("%s came back rgb(%d,%d,%d), drew rgb(%d,%d,%d) — "+
				"a quadrant in the wrong place means the picture is flipped, not merely off-colour",
				c.name, r>>8, g>>8, bb>>8, c.r, c.g, c.b)
		}
	}
}

func abs8(a uint32, b uint8) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

func TestTheDPIDecidesHowBigThePaperIs(t *testing.T) {
	// A picture has pixels; a page has points. 2480x3508 is A4 at 300 dots to
	// the inch and a page a metre tall at 72, and only the DPI says which.
	pdf, err := ToPDF([]image.Image{image.NewNRGBA(image.Rect(0, 0, 2480, 3508))}, Options{DPI: 300})
	if err != nil {
		t.Fatal(err)
	}
	w, h := mediaBox(t, pdf)
	if !near(w, pdfkit.A4.Width, 1) || !near(h, pdfkit.A4.Height, 1) {
		t.Errorf("a 300-dpi A4 scan came out %.1fx%.1f points, A4 is %.1fx%.1f",
			w, h, pdfkit.A4.Width, pdfkit.A4.Height)
	}
}

func TestAPictureFittedToPaperKeepsItsProportions(t *testing.T) {
	// ⛔ The decision this test exists for: ONE scale for both axes. Scaling
	// each to fill the page gives a full-looking page and a stretched picture,
	// which is a defect nobody reports.
	wide := image.NewNRGBA(image.Rect(0, 0, 400, 100)) // 4:1
	pdf, err := ToPDF([]image.Image{wide}, Options{DPI: 72, Page: pdfkit.A4})
	if err != nil {
		t.Fatal(err)
	}
	w, h := mediaBox(t, pdf)
	if !near(w, pdfkit.A4.Width, 1) || !near(h, pdfkit.A4.Height, 1) {
		t.Fatalf("asking for A4 gave %.1fx%.1f", w, h)
	}
	_, r := placement(400, 100, 72, Options{Page: pdfkit.A4})
	if got := r.Width / r.Height; !near(got, 4, 0.01) {
		t.Errorf("a 4:1 picture was placed at %.3f:1 — it was stretched to fill the page", got)
	}
	if !near(r.X+r.Width/2, pdfkit.A4.Width/2, 0.01) || !near(r.Y+r.Height/2, pdfkit.A4.Height/2, 0.01) {
		t.Errorf("the picture is not centred: %+v on %.1fx%.1f", r, pdfkit.A4.Width, pdfkit.A4.Height)
	}
}

func TestASmallPictureIsNotBlownUpToFillThePaper(t *testing.T) {
	// "Fit on this paper" is not "make it as large as this paper". A 32-pixel
	// logo stretched over A4 is a poster nobody asked for.
	_, r := placement(32, 32, 72, Options{Page: pdfkit.A4})
	if !near(r.Width, 32, 0.01) || !near(r.Height, 32, 0.01) {
		t.Errorf("a 32x32 picture was placed at %.1fx%.1f points", r.Width, r.Height)
	}
}

func TestAMarginWiderThanThePaperDoesNotEraseThePicture(t *testing.T) {
	// ⛔ A margin of 500 points on A4 leaves negative room. Computing with it
	// gives a zero- or negative-sized rectangle, and the page comes out blank
	// with no error: the picture is the content, the margin is a preference,
	// so the preference is what gives way.
	_, r := placement(100, 100, 72, Options{Page: pdfkit.A4, Margin: 500})
	if r.Width <= 0 || r.Height <= 0 {
		t.Errorf("an impossible margin left a %.1fx%.1f picture", r.Width, r.Height)
	}
}

func TestEachPicturePagesOnItsOwnSizeWhenNoPaperIsNamed(t *testing.T) {
	// Scans of different sizes must not all be forced onto one paper because
	// nobody said anything. "" is an instruction, not a missing value.
	s, err := ParsePageSize("")
	if err != nil {
		t.Fatal(err)
	}
	if s.Width != 0 || s.Height != 0 {
		t.Errorf(`ParsePageSize("") gave %v, which is a paper size rather than "size to the picture"`, s)
	}
	pdf, err := ToPDF([]image.Image{
		image.NewNRGBA(image.Rect(0, 0, 100, 200)),
		image.NewNRGBA(image.Rect(0, 0, 300, 150)),
	}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	boxes := mediaBoxes(t, pdf)
	if len(boxes) != 2 {
		t.Fatalf("%d media boxes for two pictures", len(boxes))
	}
	if !near(boxes[0][0], 100, 1) || !near(boxes[1][0], 300, 1) {
		t.Errorf("the two pages are %v — they were forced onto one size", boxes)
	}
}

func TestNoPicturesIsRefusedRatherThanWrittenAsAnEmptyPDF(t *testing.T) {
	// ⛔ An empty PDF is a file, and a caller who passed an empty slice by
	// mistake sees a file and believes it worked.
	if _, err := ToPDF(nil, Options{}); err == nil {
		t.Error("a PDF of no pages was written")
	}
	if _, err := ReadersToPDF(nil, Options{}); err == nil {
		t.Error("a PDF of no pages was written from no readers")
	}
}

func TestAPictureTooLargeToHoldIsRefused(t *testing.T) {
	// The size is decided by the FILE. Materialising whatever it asks for is an
	// allocation the input chooses.
	m := image.NewNRGBA(image.Rect(0, 0, 2000, 2000))
	if _, err := ToPDF([]image.Image{m}, Options{MaxPixels: 1000}); err == nil {
		t.Error("a four-megapixel picture was accepted under a thousand-pixel ceiling")
	}
	if _, err := ToPDF([]image.Image{m}, Options{}); err != nil {
		t.Errorf("the same picture was refused under the default ceiling: %v", err)
	}
}

func TestWebPIsRefusedByNameRatherThanAsUnknown(t *testing.T) {
	// ⛔ Two different refusals. WebP can be READ here and there is no pure-Go
	// encoder, so "I do not know that format" would be a lie, and quietly
	// writing a PNG under a .webp name would be worse than either.
	_, err := encoderFor("webp")
	if err == nil {
		t.Fatal("webp was accepted as something to write")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("webp was refused as %q, which reads like an unknown format rather than "+
			"one that can be read and not written", err)
	}
	_, err = encoderFor("nonesuch")
	if err == nil {
		t.Fatal("an invented format was accepted")
	}
	if strings.Contains(err.Error(), "read here") {
		t.Errorf("an unknown format was refused as %q, which is the message for webp", err)
	}
}

func TestTheSpellingOfAFormatDoesNotMatter(t *testing.T) {
	for _, n := range []string{"jpg", "JPG", "jpeg", ".jpg", " Jpeg ", "jfif"} {
		if _, err := encoderFor(n); err != nil {
			t.Errorf("%q was refused: %v", n, err)
		}
	}
	if canonical("TIF") != "tiff" {
		t.Errorf("TIF resolved to %q", canonical("TIF"))
	}
}

func TestTheContentDecidesTheFormatNotTheName(t *testing.T) {
	// ⛔ A scanner writing JPEG into "page.png" is ordinary. A converter that
	// trusted the extension would hand the decoder the wrong reader, or refuse
	// a file it can read perfectly well.
	var buf bytes.Buffer
	if err := png.Encode(&buf, swatch(8, 8)); err != nil {
		t.Fatal(err)
	}
	_, format, err := DecodeBytes(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" {
		t.Errorf("PNG bytes were read as %q", format)
	}
}

func TestAPaperSizeIsReadByNameOrByPoints(t *testing.T) {
	a4, err := ParsePageSize("a4")
	if err != nil {
		t.Fatal(err)
	}
	land, err := ParsePageSize("a4-landscape")
	if err != nil {
		t.Fatal(err)
	}
	if !near(land.Width, a4.Height, 0.01) || !near(land.Height, a4.Width, 0.01) {
		t.Errorf("a4-landscape is %v, a4 is %v", land, a4)
	}
	pts, err := ParsePageSize("200x400")
	if err != nil {
		t.Fatal(err)
	}
	if !near(pts.Width, 200, 0.01) || !near(pts.Height, 400, 0.01) {
		t.Errorf("200x400 points gave %v", pts)
	}
	for _, bad := range []string{"a9", "0x100", "-5x10", "wide"} {
		if _, err := ParsePageSize(bad); err == nil {
			t.Errorf("%q was accepted as a paper size", bad)
		}
	}
}

func TestAPageNumberOutOfRangeSaysHowManyThereAre(t *testing.T) {
	pdf, err := ToPDF([]image.Image{swatch(10, 10)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = FromPDF(pdf, RasterOptions{Pages: []int{4}})
	if err == nil {
		t.Fatal("page 4 of a one-page file was drawn")
	}
	if !strings.Contains(err.Error(), "1") {
		t.Errorf("the refusal is %q and does not say how many pages there are", err)
	}
}

func TestEveryFormatThisPackageClaimsToWriteActuallyWrites(t *testing.T) {
	// ⛔ A table of encoders is a claim. This runs each one, because an entry
	// that panics or errors is indistinguishable from one that works until
	// somebody asks for it.
	pdf, err := ToPDF([]image.Image{swatch(40, 30)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	formats := Encoders()
	if len(formats) < 5 {
		t.Fatalf("only %d formats claimed: %v", len(formats), formats)
	}
	for _, f := range formats {
		ps, err := FromPDF(pdf, RasterOptions{Format: f, DPI: 72})
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if len(ps) != 1 || len(ps[0].Data) == 0 {
			t.Errorf("%s wrote %d bytes", f, len(ps[0].Data))
			continue
		}
		if ps[0].Format != f {
			t.Errorf("asked for %s and was told %s", f, ps[0].Format)
		}
		// And it has to be readable again, by a decoder that is not ours.
		if _, got, err := DecodeBytes(ps[0].Data); err != nil {
			t.Errorf("%s came back unreadable: %v", f, err)
		} else if canonical(got) != f {
			t.Errorf("%s was written and reads back as %s", f, got)
		}
	}
}

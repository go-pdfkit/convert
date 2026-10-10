// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/go-pdfkit/pdfkit"
)

// pageOf makes a one-page PDF whose page is entirely one colour, by laying a
// picture of that colour on a page sized to it. It is the simplest subject a
// pixel test can have: every pixel of the result should be a known value.
func pageOf(t *testing.T, c color.NRGBA, w, h int) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetNRGBA(x, y, c)
		}
	}
	pdf, err := ToPDF([]image.Image{m}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	return pdf
}

// middle draws a redrawn PDF and returns the colour at the centre of page one.
//
// ⛔ It goes all the way back to PIXELS. Every defect this file is about —
// a background that painted nothing, an inversion that inverted nothing —
// leaves the byte count and the page count exactly as they were.
func middle(t *testing.T, pdf []byte) color.NRGBA {
	t.Helper()
	ps, err := FromPDF(pdf, RasterOptions{DPI: 36, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) == 0 {
		t.Fatal("no pages came back")
	}
	m, err := png.Decode(bytes.NewReader(ps[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	b := m.Bounds()
	r, g, bb, a := m.At(b.Min.X+b.Dx()/2, b.Min.Y+b.Dy()/2).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bb >> 8), A: uint8(a >> 8)}
}

// close8 is `near` for eight-bit channels. The package already has a `near`
// for floats, in convert_test.go.
func close8(a, b uint8, tol int) bool {
	d := int(a) - int(b)
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestInvertTurnsAWhitePageBlack(t *testing.T) {
	src := pageOf(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 40, 40)
	out, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72}, Invert: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := middle(t, out)
	if !close8(got.R, 0, 12) || !close8(got.G, 0, 12) || !close8(got.B, 0, 12) {
		t.Errorf("a white page inverted to %v", got)
	}
}

func TestGreyscaleLeavesTheThreeChannelsEqual(t *testing.T) {
	src := pageOf(t, color.NRGBA{R: 220, G: 40, B: 40, A: 255}, 40, 40)
	out, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72}, Greyscale: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := middle(t, out)
	if !close8(got.R, got.G, 3) || !close8(got.G, got.B, 3) {
		t.Errorf("a red page came back as %v, which still has colour in it", got)
	}
	// And it is not simply black or white: the red had a luminance.
	if got.R < 20 || got.R > 230 {
		t.Errorf("the grey is %d, which is not what a mid red weighs", got.R)
	}
}

func TestABackgroundColourActuallyReachesThePage(t *testing.T) {
	// ⛔ The defect this test exists for. A first version painted the colour
	// UNDER the finished raster — which does nothing, because the renderer has
	// already filled the page with opaque white and left no transparency to
	// show through. The option was documented and shipped and had no effect;
	// the byte count and the page count were both exactly right, and a
	// screenshot is what found it.
	// ⛔ The page has to be BIGGER than its content, or there is nowhere for a
	// background to show: a page sized to its picture is covered by it, and
	// the first version of this test sampled a corner that was the picture.
	m := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			m.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	src, err := ToPDF([]image.Image{m}, Options{DPI: 72, Page: pdfkit.A5, Margin: 40})
	if err != nil {
		t.Fatal(err)
	}
	cream := color.RGBA{R: 255, G: 238, B: 204, A: 255}
	out, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72}, Background: &cream,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The colour shows in the MARGIN the picture does not reach.
	ps, err := FromPDF(out, RasterOptions{DPI: 36, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	drawn, err := png.Decode(bytes.NewReader(ps[0].Data))
	if err != nil {
		t.Fatal(err)
	}
	b := drawn.Bounds()
	r, g, bb, _ := drawn.At(b.Min.X+1, b.Min.Y+1).RGBA()
	got := color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bb >> 8)}
	if close8(got.R, 255, 2) && close8(got.G, 255, 2) && close8(got.B, 255, 2) {
		t.Errorf("the corner is still white (%v): the background was painted nowhere", got)
	}
	if !close8(got.R, cream.R, 12) || !close8(got.G, cream.G, 12) || !close8(got.B, cream.B, 12) {
		t.Errorf("the corner is %v, asked for %v", got, cream)
	}
}

func TestBrightnessAndContrastMoveThePixelsTheRightWay(t *testing.T) {
	mid := color.NRGBA{R: 128, G: 128, B: 128, A: 255}
	src := pageOf(t, mid, 40, 40)

	up, err := Redraw(src, RedrawOptions{RasterOptions: RasterOptions{DPI: 72}, Brightness: 0.4})
	if err != nil {
		t.Fatal(err)
	}
	down, err := Redraw(src, RedrawOptions{RasterOptions: RasterOptions{DPI: 72}, Brightness: -0.4})
	if err != nil {
		t.Fatal(err)
	}
	if middle(t, up).R <= 140 {
		t.Errorf("brightening mid grey gave %v", middle(t, up))
	}
	if middle(t, down).R >= 116 {
		t.Errorf("darkening mid grey gave %v", middle(t, down))
	}

	// ⛔ Contrast on mid grey is a no-op by definition — it is the pivot — so
	// a test of contrast has to use something that is NOT mid grey, or it
	// passes for any implementation at all.
	light := pageOf(t, color.NRGBA{R: 200, G: 200, B: 200, A: 255}, 40, 40)
	hard, err := Redraw(light, RedrawOptions{RasterOptions: RasterOptions{DPI: 72}, Contrast: 2})
	if err != nil {
		t.Fatal(err)
	}
	if middle(t, hard).R <= 205 {
		t.Errorf("doubling the contrast of a light grey gave %v", middle(t, hard))
	}
}

func TestAContrastOfZeroMeansOneAndNotAFlatGreyPage(t *testing.T) {
	// ⛔ RedrawOptions{Invert: true} leaves Contrast at its zero value, and a
	// zero read as "no contrast at all" would turn every page into a flat
	// rectangle for every caller who set only the other fields.
	src := pageOf(t, color.NRGBA{R: 30, G: 30, B: 30, A: 255}, 40, 40)
	out, err := Redraw(src, RedrawOptions{RasterOptions: RasterOptions{DPI: 72}})
	if err != nil {
		t.Fatal(err)
	}
	if got := middle(t, out); !close8(got.R, 30, 12) {
		t.Errorf("a dark page redrawn with no options came back %v", got)
	}
}

func TestTheScannerEffectIsReproducible(t *testing.T) {
	// ⛔ Two runs over the same file must produce the same bytes, or nothing
	// downstream can be compared, cached or checksummed — and an effect that
	// is different every time is one nobody can test. The zero Seed is a FIXED
	// seed, not a random one.
	src := pageOf(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 40, 40)
	opt := RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72},
		Scanner:       &ScannerEffect{Skew: 0.5, Noise: 0.2, Fade: 0.1},
	}
	a, err := Redraw(src, opt)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Redraw(src, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two runs with the same options produced different bytes")
	}
	// And a different seed is a different page, or the seed does nothing.
	opt.Scanner = &ScannerEffect{Skew: 0.5, Noise: 0.2, Fade: 0.1, Seed: 99}
	c, err := Redraw(src, opt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, c) {
		t.Error("changing the seed changed nothing")
	}
}

func TestEachPageGetsItsOwnGrain(t *testing.T) {
	// ⛔ Without the page index in the seed every page of a document gets the
	// identical grain — the one thing a scanner never does, and the first
	// thing anybody notices.
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	m := image.NewNRGBA(image.Rect(0, 0, 30, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 30; x++ {
			m.SetNRGBA(x, y, white)
		}
	}
	src, err := ToPDF([]image.Image{m, m}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72},
		Scanner:       &ScannerEffect{Noise: 0.3},
	})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := FromPDF(out, RasterOptions{DPI: 36, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("%d pages", len(ps))
	}
	if bytes.Equal(ps[0].Data, ps[1].Data) {
		t.Error("two pages of the same document came back with identical grain")
	}
}

func TestValuesOutOfRangeAreRefusedAndSayWhat(t *testing.T) {
	src := pageOf(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 20, 20)
	for _, c := range []struct {
		name string
		opt  RedrawOptions
		want string
	}{
		{"brightness", RedrawOptions{Brightness: 2}, "brightness"},
		{"brightness low", RedrawOptions{Brightness: -2}, "brightness"},
		{"contrast", RedrawOptions{Contrast: -1}, "contrast"},
		{"noise", RedrawOptions{Scanner: &ScannerEffect{Noise: 3}}, "noise"},
		{"fade", RedrawOptions{Scanner: &ScannerEffect{Fade: -1}}, "fade"},
		{"skew", RedrawOptions{Scanner: &ScannerEffect{Skew: 90}}, "skew"},
	} {
		_, err := Redraw(src, c.opt)
		if err == nil {
			t.Errorf("%s out of range was accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s was refused as %q", c.name, err)
		}
	}
}

func TestRedrawingKeepsThePageSize(t *testing.T) {
	// ⛔ Drawing at 150 and laying out at 72 hands back a PDF whose pages are
	// twice the size of the ones that went in. The content is right and every
	// page is wrong, which is the kind of defect that survives a review.
	src := pageOf(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 300, 150)
	before := mediaBoxes(t, src)
	out, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 200}, Greyscale: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after := mediaBoxes(t, out)
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("%d before, %d after", len(before), len(after))
	}
	if !nearF(before[0][0], after[0][0], 1) || !nearF(before[0][1], after[0][1], 1) {
		t.Errorf("a %.0fx%.0f page came back %.0fx%.0f",
			before[0][0], before[0][1], after[0][0], after[0][1])
	}
}

func nearF(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestOnlyTheSelectedPagesComeBack(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	m := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			m.SetNRGBA(x, y, white)
		}
	}
	src, err := ToPDF([]image.Image{m, m, m}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72, Pages: []int{2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(mediaBoxes(t, out)); n != 1 {
		t.Errorf("asking for one page gave %d", n)
	}
	if _, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72, Pages: []int{9}},
	}); err == nil {
		t.Error("page 9 of a three-page file was drawn")
	}
}

func TestTheDefaultDPIIsTheOneDocumented(t *testing.T) {
	// ⛔ Zero means 150, not zero. A DPI read as zero would draw a page of no
	// pixels, and the refusal would come from somewhere far away and be about
	// something else.
	src := pageOf(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 72, 72)
	out, err := Redraw(src, RedrawOptions{Greyscale: true})
	if err != nil {
		t.Fatalf("a redraw with no DPI was refused: %v", err)
	}
	// A 72x72-point page at 150 dpi is 150 pixels, laid back out at 150 dpi:
	// 72 points again. The size surviving is what says the default was used
	// on BOTH sides rather than on one.
	b := mediaBoxes(t, out)
	if len(b) != 1 || !nearF(b[0][0], 72, 1.5) {
		t.Errorf("the page came back %v", b)
	}
}

func TestDarkeningPastBlackStopsAtBlack(t *testing.T) {
	// Clamping is not decoration: an unclamped subtraction wraps around in
	// eight bits and a very dark page comes back very light.
	src := pageOf(t, color.NRGBA{R: 20, G: 20, B: 20, A: 255}, 40, 40)
	out, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72},
		Scanner:       &ScannerEffect{Noise: 1, Seed: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Full noise on a near-black page drives samples below zero all over it.
	// What matters is that nothing came back inverted.
	if got := middle(t, out); got.R > 200 {
		t.Errorf("a near-black page with full grain came back at %v, which is what "+
			"wrapping around instead of clamping looks like", got)
	}
}

func TestAPageTooLargeToRedrawIsRefusedWithItsNumber(t *testing.T) {
	src := pageOf(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 40, 40)
	_, err := Redraw(src, RedrawOptions{
		RasterOptions: RasterOptions{DPI: 72, MaxPixels: 1},
	})
	if err == nil {
		t.Fatal("a page was drawn under a one-pixel ceiling")
	}
	if !strings.Contains(err.Error(), "page 1") {
		t.Errorf("the refusal is %q and does not say which page", err)
	}
}

func TestSomethingThatIsNotAPDFIsRefused(t *testing.T) {
	if _, err := Redraw([]byte("not a PDF"), RedrawOptions{}); err == nil {
		t.Error("it was accepted")
	}
	if _, err := Redraw([]byte(aPDFWithNoPages), RedrawOptions{}); err == nil {
		t.Error("a PDF of no pages was redrawn")
	}
}

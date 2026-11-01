// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"errors"
	"image/color"
	"strings"
	"testing"

	gfxsvg "github.com/go-gfx/gfx/svg"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// The documents here are flat-colour paths — icons, logos, diagrams — which is
// what people convert an SVG to PDF for and what the vector path covers.

const vecDoc = `<svg viewBox="0 0 80 60">
  <rect x="5" y="5" width="30" height="20" fill="#2040c0"/>
  <path d="M45 10 C 60 10, 75 25, 60 40 L 45 40 Z" fill="#c04020"/>
  <path d="M5 50 L 75 50" stroke="#108020" stroke-width="4" stroke-linecap="round"/>
  <path d="M10 35 Q 20 28 30 35" fill="none" stroke="#806000" stroke-width="2"/>
</svg>`

func TestSVGToPDFKeepsThePathsAsPaths(t *testing.T) {
	b, err := SVGToPDFVector([]byte(vecDoc), Options{})
	if err != nil {
		t.Fatalf("the document is plain paths and was refused: %v", err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatalf("our own reader cannot open what we wrote: %v", err)
	}
	if d.PageCount() != 1 {
		t.Fatalf("%d pages, want 1", d.PageCount())
	}

	// ⛔ THE CLAIM IS THAT THE PATHS SURVIVED, so the content stream is read
	// back and the painting operators counted. A test that only compared
	// pixels would pass for a rasterised page, which is the thing this exists
	// not to produce.
	ops, err := d.PageOperations(1)
	if err != nil {
		t.Fatal(err)
	}
	var curves, fills, strokes, images int
	for _, op := range ops {
		switch op.Operator {
		case "c":
			curves++
		case "f", "f*":
			fills++
		case "S":
			strokes++
		case "Do":
			images++
		}
	}
	if images != 0 {
		t.Errorf("%d XObjects drawn: the page is a picture, not paths", images)
	}
	if curves == 0 {
		t.Error("no curve operators: the beziers did not survive")
	}
	// ⛔ THREE FILLS, NOT TWO, and the third one caught me assuming. In SVG the
	// initial value of `fill` is BLACK, not none — so the stroked line that
	// names no fill is filled as well, degenerately, and go-gfx's rasteriser
	// fills it too. Only the path that says fill="none" is stroke-only.
	// Emitting it keeps this faithful to the drawing rather than to my idea of
	// what the document meant.
	if fills != 3 {
		t.Errorf("%d fills, want 3 (the rect, the closed path, and the line whose fill defaults to black)", fills)
	}
	if strokes != 2 {
		t.Errorf("%d strokes, want 2", strokes)
	}
}

// TestSVGToPDFVectorLooksLikeTheSVG is the measurement behind the claim: the
// page, drawn by our own renderer, has to look like the document drawn by
// go-gfx.
//
// ⛔ Two independent drawers of the same geometry will not agree byte for
// byte — different rasterisers, different anti-aliasing — so the comparison is
// a tolerance on how many pixels differ and by how much, not an identity. The
// control is that a BLANK page of the same size fails the same comparison
// loudly, so a tolerance wide enough to accept anything is ruled out.
func TestSVGToPDFVectorLooksLikeTheSVG(t *testing.T) {
	b, err := SVGToPDFVector([]byte(vecDoc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := render.Page(d, 1, render.Options{DPI: 144})
	if err != nil {
		t.Fatal(err)
	}
	res, err := gfxsvg.Rasterize(vecDoc, gfxsvg.Options{
		Scale: 2, Paper: color.RGBA{255, 255, 255, 255},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := res.Image

	if got.W != want.W || got.H != want.H {
		t.Fatalf("our page is %dx%d, go-gfx drew %dx%d", got.W, got.H, want.W, want.H)
	}
	bad := meanAbsDiff(got.Pix, want.Pix)
	t.Logf("mean |difference| per channel between our page and go-gfx's drawing: %.2f of 255", bad)
	if bad > 12 {
		t.Errorf("the page does not look like the document: mean difference %.2f", bad)
	}

	// The control: a blank page of the same size must FAIL this comparison, or
	// the tolerance is meaningless.
	blank := make([]uint8, len(want.Pix))
	for i := range blank {
		blank[i] = 255
	}
	if blankDiff := meanAbsDiff(blank, want.Pix); blankDiff <= 12 {
		t.Errorf("a blank page scores %.2f, inside the tolerance: the measure cannot tell drawing from nothing", blankDiff)
	} else {
		t.Logf("control: a blank page scores %.2f, well outside", blankDiff)
	}
}

func meanAbsDiff(a, b []uint8) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	total := 0.0
	for i := 0; i < n; i++ {
		d := float64(a[i]) - float64(b[i])
		if d < 0 {
			d = -d
		}
		total += d
	}
	return total / float64(n)
}

func TestSVGToPDFVectorRefusesWhatItCannotExpress(t *testing.T) {
	// ⛔ Each of these would be SILENTLY WRONG if translated: the feature
	// simply would not appear. The refusal names it so SVGToPDF can draw the
	// document instead.
	for _, tc := range []struct{ why, doc, names string }{
		{
			"a gradient",
			`<svg viewBox="0 0 10 10"><defs><linearGradient id="g">
			  <stop offset="0" stop-color="#000"/><stop offset="1" stop-color="#fff"/>
			  </linearGradient></defs><rect width="10" height="10" fill="url(#g)"/></svg>`,
			"gradient",
		},
		{
			"a clip path",
			`<svg viewBox="0 0 10 10"><defs><clipPath id="c">
			  <rect width="5" height="10"/></clipPath></defs>
			  <rect width="10" height="10" fill="#000" clip-path="url(#c)"/></svg>`,
			"clip-path",
		},
		{
			"an embedded picture",
			`<svg viewBox="0 0 10 10"><image width="10" height="10"
			  href="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"/></svg>`,
			"image",
		},
	} {
		_, err := SVGToPDFVector([]byte(tc.doc), Options{})
		if !errors.Is(err, ErrSVGNotVector) {
			t.Errorf("%s: got %v, want ErrSVGNotVector", tc.why, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.names) {
			t.Errorf("%s: the error should name it: %v", tc.why, err)
		}

		// ⛔ AND SVGToPDF STILL PRODUCES A PAGE for it, by drawing. A refusal
		// that reached the caller would be a regression against the old
		// always-rasterise behaviour.
		b, err := SVGToPDF([]byte(tc.doc), Options{})
		if err != nil {
			t.Errorf("%s: SVGToPDF did not fall back: %v", tc.why, err)
			continue
		}
		d, err := reader.Open(b)
		if err != nil {
			t.Errorf("%s: the drawn fallback does not open: %v", tc.why, err)
			continue
		}
		ops, err := d.PageOperations(1)
		if err != nil {
			t.Fatal(err)
		}
		drawn := false
		for _, op := range ops {
			if op.Operator == "Do" {
				drawn = true
			}
		}
		if !drawn {
			t.Errorf("%s: the fallback page draws no picture", tc.why)
		}
	}
}

func TestSVGToPDFUsesTheVectorPathWhenItCan(t *testing.T) {
	// The other half of the fallback: a plain document must NOT be drawn.
	b, err := SVGToPDF([]byte(vecDoc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := d.PageOperations(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Operator == "Do" {
			t.Fatal("a document of plain paths was rasterised")
		}
	}
}

func TestSVGToPDFVectorRefusesWhatIsNotAnSVG(t *testing.T) {
	for _, doc := range []string{"", "not xml", "<html/>"} {
		if _, err := SVGToPDFVector([]byte(doc), Options{}); err == nil {
			t.Errorf("%q was accepted", doc)
		} else if errors.Is(err, ErrSVGNotVector) {
			t.Errorf("%q: not-an-SVG must not read as not-vector, or SVGToPDF would try to draw it", doc)
		}
	}
	big := make([]byte, maxSourceBytes+1)
	if _, err := SVGToPDFVector(big, Options{}); err == nil {
		t.Error("a document past the byte ceiling was accepted")
	}
}

func TestSVGToPDFVectorCarriesTheStrokeAndFillSettings(t *testing.T) {
	// Each of these is a graphics-state operator that would be silently
	// dropped if the mapping forgot it, and none of them changes the geometry
	// — so the pixel comparison above would not notice. The content stream is
	// read back for the operator itself.
	const doc = `<svg viewBox="0 0 60 40">
	  <path d="M5 5 L55 5 L30 35 Z" fill="#303030" fill-rule="evenodd"
	        stroke="#a00000" stroke-width="3" stroke-miterlimit="7"
	        stroke-dasharray="6 2" stroke-dashoffset="1"/>
	  <rect x="5" y="20" width="10" height="10" fill="#0000ff" fill-opacity="0.5"/>
	</svg>`
	b, err := SVGToPDFVector([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := d.PageOperations(1)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, op := range ops {
		seen[op.Operator] = true
	}
	for _, want := range []struct{ op, why string }{
		{"f*", "the even-odd fill rule"},
		{"M", "the miter limit"},
		{"d", "the dash pattern"},
		{"w", "the stroke width"},
		{"gs", "the opacity, which lives in an ExtGState"},
		{"RG", "the stroke colour"},
		{"rg", "the fill colour"},
	} {
		if !seen[want.op] {
			t.Errorf("no %q operator: %s did not survive", want.op, want.why)
		}
	}
}

func TestSVGToPDFFallsBackWhenTheDrawingIsTooLargeForTheVectorSizeToPredict(t *testing.T) {
	// ⛔ A case that only exists because the two paths measure DIFFERENTLY.
	// The vector walk runs at Scale 1 — one user unit, one point — while the
	// drawn fallback runs at Options.DPI/72. So a document can pass the pixel
	// ceiling as vector and fail it as a drawing, and the gradient here sends
	// it down the drawn path where that happens.
	const shaded = `<svg viewBox="0 0 2000 2000"><defs><linearGradient id="g">
	  <stop offset="0" stop-color="#000"/><stop offset="1" stop-color="#fff"/>
	  </linearGradient></defs><rect width="2000" height="2000" fill="url(#g)"/></svg>`

	// At Scale 1 the surface is 4 000 000 pixels, which this ceiling allows.
	if _, err := SVGToPDFVector([]byte(shaded), Options{MaxPixels: 5_000_000}); !errors.Is(err, ErrSVGNotVector) {
		t.Fatalf("the fixture should be refused for its gradient, not its size: %v", err)
	}
	// At 288 DPI the drawing is sixteen times that and the same ceiling
	// refuses it — so SVGToPDF has nowhere left to go and must say so.
	_, err := SVGToPDF([]byte(shaded), Options{MaxPixels: 5_000_000, DPI: 288})
	if err == nil {
		t.Fatal("a document too large to draw was accepted")
	}
	if errors.Is(err, ErrSVGNotVector) {
		t.Errorf("the caller is told it is not vector, when the real problem is the size: %v", err)
	}
}

func TestRasterizeSVGKeepsItsOwnByteCeiling(t *testing.T) {
	// ⛔ TESTED DIRECTLY, because SVGToPDF no longer reaches it. The vector
	// path refuses an oversized document first, so the ceiling inside
	// RasterizeSVG stopped being exercised through the front door the day
	// SVGToPDF started trying vector first. RasterizeSVG is public and has to
	// guard itself whoever calls it — the duplication is the point, not an
	// oversight.
	if _, err := RasterizeSVG(make([]byte, maxSourceBytes+1), Options{}); err == nil {
		t.Error("a document past the byte ceiling was drawn")
	}
}

func TestSVGToPDFRefusesWhatIsNotAnSVGWithoutTryingToDrawIt(t *testing.T) {
	// Not an SVG is not the same as not vector: retrying it as a drawing would
	// fail for the same reason, so the first refusal stands.
	if _, err := SVGToPDF([]byte("certainly not an svg"), Options{}); err == nil {
		t.Error("a non-SVG was accepted")
	}
}

func TestPDFLineCapAndJoinAreMappedNotCast(t *testing.T) {
	// ISO 32000-1 Table 54 and Table 55. go-gfx happens to number its own the
	// same way today; this is what notices if either side reorders.
	if got := []int{
		pdfLineCap(vector.ButtCap), pdfLineCap(vector.RoundCap), pdfLineCap(vector.SquareCap),
	}; got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("caps map to %v, want [0 1 2] (butt, round, projecting square)", got)
	}
	if got := []int{
		pdfLineJoin(vector.MiterJoin), pdfLineJoin(vector.RoundJoin), pdfLineJoin(vector.BevelJoin),
	}; got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("joins map to %v, want [0 1 2] (miter, round, bevel)", got)
	}
}

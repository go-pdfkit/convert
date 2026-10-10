// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand/v2"

	"github.com/go-images/images"
	"github.com/go-pdfkit/render"
)

// Redraw draws every page, changes the pixels, and lays the results back into
// a new PDF.
//
// ⛔ IT RASTERISES. What comes out has no text in it: no selection, no search,
// no copy, no screen reader, and a file many times larger. That is not a
// shortcoming of this implementation — changing the colours a page is PAINTED
// in means painting it — but it is a thing a caller has to have decided on
// purpose, which is why the function is named for what it does rather than for
// what it is for.
//
// Where the operation can be done without redrawing, it belongs somewhere
// else: rotating, cropping, stamping and reordering are
// [github.com/go-pdfkit/ops], and they keep the text.
func Redraw(pdf []byte, opt RedrawOptions) ([]byte, error) {
	if err := opt.check(); err != nil {
		return nil, err
	}
	pages, err := drawPages(pdf, opt.RasterOptions, opt.Background)
	if err != nil {
		return nil, err
	}

	// ⛔ apply returns no error, and that is deliberate. A first version gave
	// it one "in case", the branch could never be taken, and it was a claim
	// nobody had tested. Everything that CAN be refused is refused by
	// opt.check() above, before a single page is drawn — which is also where
	// a caller wants to hear about it.
	imgs := make([]image.Image, 0, len(pages))
	for i, p := range pages {
		imgs = append(imgs, opt.apply(p.img, i))
	}

	dpi := opt.DPI
	if dpi <= 0 {
		dpi = 150
	}
	// ⛔ The same DPI on the way out as on the way in. Drawing at 150 and
	// laying out at 72 would hand back a PDF whose pages are twice the size of
	// the ones that went in — the content would be right and every page would
	// be wrong, which is the kind of defect that survives a review.
	return ToPDF(imgs, Options{DPI: dpi, Title: opt.Title, MaxPixels: opt.MaxPixels})
}

// RedrawOptions says what to do to the pixels. Everything is off by default:
// the zero value redraws the pages unchanged, which is `rasterize-pdf`.
type RedrawOptions struct {
	RasterOptions

	// Title goes in the new document's information dictionary.
	Title string

	// Greyscale drops the colour.
	Greyscale bool

	// Invert turns light into dark. It is what a reader wants at night, and
	// what a plotter wants of a dark-background figure.
	Invert bool

	// Brightness shifts every channel, from -1 (black) to +1 (white). Zero
	// leaves it alone.
	Brightness float64

	// Contrast multiplies the distance from mid-grey: 1 leaves it alone, 2
	// doubles it, 0.5 halves it. Zero means 1 — ⛔ a zero that meant "no
	// contrast at all" would turn every page into a flat grey rectangle for
	// every caller who set only the other fields.
	Contrast float64

	// Background is the colour the page is painted on. nil leaves the
	// renderer's own white.
	//
	// ⛔ It is handed to the RENDERER rather than composited afterwards, and
	// the difference is the whole option. A first version painted the colour
	// under the finished raster — which does nothing at all, because the
	// renderer has already filled the page with opaque white and there is no
	// transparency left to show through. The option was documented, shipped,
	// and had no effect; a screenshot found it, a byte count did not.
	Background *color.RGBA

	// Scanner, when set, makes the page look like it went through one.
	Scanner *ScannerEffect
}

// ScannerEffect is the small damage a flatbed does: the sheet is never quite
// straight, the lamp is never quite even, and the sensor adds grain.
type ScannerEffect struct {
	// Skew is how far the sheet sits off square, in degrees. A tenth to a
	// degree is what a careless hand produces.
	Skew float64

	// Noise is how much grain, from 0 to 1.
	Noise float64

	// Fade lightens the page, as a worn lamp does, from 0 to 1.
	Fade float64

	// Seed makes the grain reproducible. ⛔ Zero means a FIXED seed, not a
	// random one: two runs over the same file must produce the same bytes, or
	// nothing downstream can be compared, cached or checksummed — and an
	// effect that is different every time is one nobody can test.
	Seed uint64
}

func (o RedrawOptions) check() error {
	if o.Brightness < -1 || o.Brightness > 1 {
		return fmt.Errorf("brightness is -1 to 1, not %g", o.Brightness)
	}
	if o.Contrast < 0 {
		return fmt.Errorf("contrast is zero or more, not %g", o.Contrast)
	}
	if s := o.Scanner; s != nil {
		if s.Noise < 0 || s.Noise > 1 {
			return fmt.Errorf("scanner noise is 0 to 1, not %g", s.Noise)
		}
		if s.Fade < 0 || s.Fade > 1 {
			return fmt.Errorf("scanner fade is 0 to 1, not %g", s.Fade)
		}
		if math.Abs(s.Skew) > 45 {
			return fmt.Errorf("a scanner skew of %g degrees is not a scanner", s.Skew)
		}
	}
	return nil
}

// apply runs the transforms in a fixed order.
//
// ⛔ The order is part of the answer, not an implementation detail. Greyscale
// after a contrast change is not the same picture as contrast after greyscale,
// and a caller who set both would otherwise be guessing. Written down: colour
// first, then tone, then the scanner's damage last — because a scanner damages
// whatever it is shown, and the page is what it is shown.
func (o RedrawOptions) apply(src image.Image, index int) image.Image {
	// The background is not here: it is painted by the renderer, before the
	// page is drawn. See RedrawOptions.Background.
	m := image.Image(src)
	if o.Greyscale {
		m = images.Grayscale(m)
	}
	if o.Invert {
		m = images.Invert(m)
	}
	if o.Brightness != 0 {
		// ⛔ images.AdjustBrightness adds its delta in CHANNEL UNITS, 0 to
		// 255 — not in the -1..1 this option is documented in. A first
		// version passed the fraction straight through, so a brightness of
		// 0.4 added 0.4 of a level out of 255 and changed nothing visible.
		// Nothing errored. A test that read the pixels found it; a test that
		// checked the byte count would not have.
		m = images.AdjustBrightness(m, o.Brightness*255)
	}
	if c := o.Contrast; c != 0 && c != 1 {
		m = images.AdjustContrast(m, c)
	}
	if s := o.Scanner; s != nil {
		m = s.apply(m, index)
	}
	return m
}

// under paints a colour beneath the picture, so that whatever was transparent
// becomes that colour rather than whatever happens to be behind the page.
// ⛔ There is no compositing helper here any more. One was written — paint the
// colour under the finished raster — and it did nothing whatever, because the
// renderer fills the page with opaque white before drawing and leaves no
// transparency to show through. The colour goes to the RENDERER.

func clamp8(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(v + 0.5)
	}
}

// apply damages one page.
func (s ScannerEffect) apply(src image.Image, index int) image.Image {
	m := image.Image(src)
	if s.Skew != 0 {
		// resize=false: a scanned sheet keeps the scanner's bed, not the
		// sheet's own bounding box, so the corners go under the lid.
		m = images.Rotate(m, s.Skew, false)
	}
	if s.Fade > 0 {
		// Channel units again: a fade of 1 lifts the page by a third of the
		// range and takes a third of the contrast out of it.
		m = images.AdjustBrightness(m, s.Fade*0.35*255)
		m = images.AdjustContrast(m, 1-s.Fade*0.35)
	}
	if s.Noise > 0 {
		m = s.grain(m, index)
	}
	return m
}

// grain adds reproducible sensor noise.
//
// ⛔ The page INDEX is mixed into the seed. Without it every page of a
// document gets the identical grain, which is the one thing a scanner never
// does and the first thing anybody notices.
func (s ScannerEffect) grain(src image.Image, index int) image.Image {
	seed := s.Seed
	if seed == 0 {
		seed = 0x5EED5CA11ED
	}
	rng := rand.New(rand.NewPCG(seed, uint64(index)+1))
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	amp := s.Noise * 48
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bb, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// One sample per pixel, not per channel: a scanner's grain is
			// luminance, and per-channel noise reads as colour confetti.
			n := (rng.Float64()*2 - 1) * amp
			out.SetNRGBA(x, y, color.NRGBA{
				R: clamp8(float64(r>>8) + n),
				G: clamp8(float64(g>>8) + n),
				B: clamp8(float64(bb>>8) + n),
				A: uint8(a >> 8),
			})
		}
	}
	return out
}

// drawnPage is one rendered page and the number it had.
type drawnPage struct {
	number int
	img    image.Image
}

// drawPages renders the pages a RasterOptions selects.
//
// ⛔ It draws straight to pixels rather than going through [FromPDF]. The
// first version encoded every page to PNG and decoded it again — work thrown
// away twice over, and on a three-hundred-page file it is most of the run.
// These pixels never become a file of their own, so RasterOptions.Format is
// not consulted: a caller who wants files wants FromPDF.
func drawPages(pdf []byte, opt RasterOptions, bg *color.RGBA) ([]drawnPage, error) {
	dpi := opt.DPI
	if dpi <= 0 {
		dpi = 150
	}
	doc, err := open(pdf, opt.Password)
	if err != nil {
		return nil, fmt.Errorf("opening the PDF: %w", err)
	}
	total := doc.PageCount()
	if total == 0 {
		return nil, fmt.Errorf("the PDF opened and holds no pages")
	}
	want := opt.Pages
	if len(want) == 0 {
		want = make([]int, total)
		for i := range want {
			want[i] = i + 1
		}
	}
	out := make([]drawnPage, 0, len(want))
	for _, n := range want {
		if n < 1 || n > total {
			return nil, fmt.Errorf("page %d: this file has %d", n, total)
		}
		img, err := render.Page(doc, n, render.Options{
			DPI: dpi, MaxPixels: opt.MaxPixels, Background: bg,
		})
		if err != nil {
			return nil, fmt.Errorf("drawing page %d: %w", n, err)
		}
		out = append(out, drawnPage{number: n, img: img.ToNRGBA()})
	}
	return out, nil
}

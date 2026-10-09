// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"fmt"
	"image"
	"io"

	"github.com/go-pdfkit/pdfkit"
)

// Options says how pictures become pages.
type Options struct {
	// DPI is how many pixels of the picture go to an inch of paper. Zero means
	// 72, at which one pixel is one point and a picture comes out at the size
	// a PDF viewer calls 100%.
	//
	// It is what turns pixels into a page: a 2480x3508 scan at 300 is A4, and
	// the same scan at 72 is a page a metre tall. There is no right answer this
	// package could pick for you, only a sane default.
	DPI float64

	// Page, when set, is the page size every picture is placed on, and the
	// picture is FITTED inside it: scaled down to fit if it is too big, centred
	// either way, never cropped and never stretched out of proportion.
	//
	// When it is the zero value each page is sized to its own picture, which is
	// what a set of scans of different sizes needs.
	Page pdfkit.PageSize

	// Margin is how much paper is left around a fitted picture, in points. It
	// is only consulted when Page is set, because a page sized to its picture
	// has no room to leave.
	Margin float64

	// Title goes in the document information dictionary.
	Title string

	// MaxPixels refuses a picture larger than this, so that a file cannot
	// decide how much memory this package uses. Zero means a hundred million,
	// which is A4 at a thousand dots to the inch and well past any scanner.
	MaxPixels int
}

const defaultMaxPixels = 100_000_000

// ToPDF lays each picture on a page of its own, in the order given.
func ToPDF(images []image.Image, opt Options) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteTo(&buf, images, opt); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteTo is [ToPDF] straight to a writer, so a conversion of a hundred scans
// does not hold the finished PDF in memory as well as the pictures.
func WriteTo(w io.Writer, images []image.Image, opt Options) error {
	// ⛔ No pages is not an empty PDF. A reader opening a PDF of nothing gets
	// an error from somewhere further away with none of this context in it, and
	// a caller that passed an empty slice by mistake sees a file and believes
	// it worked.
	if len(images) == 0 {
		return fmt.Errorf("no pictures: a PDF of no pages is not a conversion")
	}
	dpi := opt.DPI
	if dpi <= 0 {
		dpi = 72
	}
	max := opt.MaxPixels
	if max <= 0 {
		max = defaultMaxPixels
	}

	doc := pdfkit.New(pdfkit.Options{Title: opt.Title})
	for i, m := range images {
		if m == nil {
			return fmt.Errorf("picture %d is nil", i+1)
		}
		b := m.Bounds()
		w, h := b.Dx(), b.Dy()
		if w <= 0 || h <= 0 {
			return fmt.Errorf("picture %d is %dx%d, which is not a picture", i+1, w, h)
		}
		if w*h > max {
			return fmt.Errorf("picture %d is %dx%d (%d pixels), past the %d this is allowed to hold",
				i+1, w, h, w*h, max)
		}
		size, r := placement(w, h, dpi, opt)
		doc.AddPage(size).DrawImage(m, r)
	}
	if err := doc.Write(w); err != nil {
		return fmt.Errorf("writing the PDF: %w", err)
	}
	return nil
}

// placement decides the page and where on it the picture goes.
//
// Two cases, and they are genuinely different rather than one with a special
// value: a page sized TO the picture has no choice to make, and a fixed page
// has to fit the picture inside it without distorting it.
func placement(w, h int, dpi float64, opt Options) (pdfkit.PageSize, pdfkit.Rect) {
	pw := float64(w) * 72 / dpi
	ph := float64(h) * 72 / dpi

	if opt.Page.Width <= 0 || opt.Page.Height <= 0 {
		return pdfkit.NewPageSize(pw, ph), pdfkit.Rect{X: 0, Y: 0, Width: pw, Height: ph}
	}

	avail := opt.Page
	mw := avail.Width - 2*opt.Margin
	mh := avail.Height - 2*opt.Margin
	if mw <= 0 || mh <= 0 {
		// A margin wider than the paper leaves nothing to draw on. Rather than
		// emit a zero-sized image, ignore it: the picture is the content, the
		// margin is a preference.
		mw, mh = avail.Width, avail.Height
		opt.Margin = 0
	}
	// ⛔ ONE scale for both axes. Scaling them separately fills the page and
	// stretches the picture, and a stretched scan is a defect nobody reports
	// because the page looks full.
	s := mw / pw
	if t := mh / ph; t < s {
		s = t
	}
	// Only ever shrink. Blowing a small picture up to fill A4 turns a logo into
	// a poster, which is not what "fit on this paper" asks for.
	if s > 1 {
		s = 1
	}
	dw, dh := pw*s, ph*s
	return avail, pdfkit.Rect{
		X:      (avail.Width - dw) / 2,
		Y:      (avail.Height - dh) / 2,
		Width:  dw,
		Height: dh,
	}
}

// ReadersToPDF decodes each reader and lays the pictures out as [ToPDF] does.
// The format of each is decided by its CONTENT, never by a name.
func ReadersToPDF(rs []io.Reader, opt Options) ([]byte, error) {
	if len(rs) == 0 {
		return nil, fmt.Errorf("no pictures: a PDF of no pages is not a conversion")
	}
	ms := make([]image.Image, 0, len(rs))
	for i, r := range rs {
		m, _, err := Decode(r)
		if err != nil {
			return nil, fmt.Errorf("picture %d: %w", i+1, err)
		}
		ms = append(ms, m)
	}
	return ToPDF(ms, opt)
}

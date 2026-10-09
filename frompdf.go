// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"fmt"
	"io"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// RasterOptions says how pages become pictures.
type RasterOptions struct {
	// DPI is dots per inch. Zero means 150, which is legible on a screen and
	// not so large that a hundred-page file fills a disk.
	DPI float64

	// Pages selects which pages to draw, one-based, in the order given. Nil
	// means every page in the file's own order.
	Pages []int

	// Format is what to encode as: png, jpeg, gif, bmp or tiff. Empty means
	// png, which is lossless and what a page of text should be.
	Format string

	// Quality is the JPEG quality, 1 to 100. Zero means 85. It is ignored by
	// every other format.
	Quality int

	// Password opens an encrypted file.
	Password string

	// MaxPixels refuses a page that would come out larger than this. Zero
	// leaves it to the renderer, whose own default is a little over A4 at 600
	// dots to the inch.
	MaxPixels int
}

// Page is one drawn page and the bytes it encoded to.
type Page struct {
	Number int    // one-based, as the file numbers it
	Format string // the format actually written
	Data   []byte
}

// FromPDF draws the pages of a PDF and encodes each one.
func FromPDF(pdf []byte, opt RasterOptions) ([]Page, error) {
	enc, format, err := resolveEncoder(opt.Format)
	if err != nil {
		return nil, err
	}
	quality := opt.Quality
	if quality <= 0 {
		quality = 85
	}
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
		// ⛔ Said out loud. A PDF that opened and holds no pages would
		// otherwise come back as an empty slice and no error, which reads
		// exactly like a selection that matched nothing.
		return nil, fmt.Errorf("the PDF opened and holds no pages")
	}

	want := opt.Pages
	if len(want) == 0 {
		want = make([]int, total)
		for i := range want {
			want[i] = i + 1
		}
	}

	out := make([]Page, 0, len(want))
	for _, n := range want {
		if n < 1 || n > total {
			return nil, fmt.Errorf("page %d: this file has %d", n, total)
		}
		// render.Page counts from ONE, like the file does and like the page
		// numbers a person types. The first draft passed n-1 out of habit and
		// drew nothing at all.
		img, err := render.Page(doc, n, render.Options{DPI: dpi, MaxPixels: opt.MaxPixels})
		if err != nil {
			return nil, fmt.Errorf("drawing page %d: %w", n, err)
		}
		var buf bytes.Buffer
		if err := enc(&buf, img.ToNRGBA(), quality); err != nil {
			return nil, fmt.Errorf("encoding page %d as %s: %w", n, format, err)
		}
		out = append(out, Page{Number: n, Format: format, Data: buf.Bytes()})
	}
	return out, nil
}

// open reads a PDF, with or without a password.
//
// ⛔ The two entry points are kept apart on purpose: OpenWithPassword("") is
// not the same request as Open(), and conflating them would turn "this file
// needs a password" into "the empty password is wrong".
func open(pdf []byte, password string) (*reader.Document, error) {
	if password == "" {
		return reader.Open(pdf)
	}
	return reader.OpenWithPassword(pdf, password)
}

// resolveEncoder turns a possibly-empty format name into an encoder and the
// canonical name it will be written under, so a caller is never told "png" for
// a file it asked to be "jpg".
func resolveEncoder(name string) (Encoder, string, error) {
	if name == "" {
		name = "png"
	}
	enc, err := encoderFor(name)
	if err != nil {
		return nil, "", err
	}
	return enc, canonical(name), nil
}

// FromPDFReader is [FromPDF] over a reader, for a file on disk.
func FromPDFReader(r io.Reader, opt RasterOptions) ([]Page, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading the PDF: %w", err)
	}
	return FromPDF(b, opt)
}

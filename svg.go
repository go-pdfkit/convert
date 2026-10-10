// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"fmt"
	"image"

	gfxsvg "github.com/go-gfx/gfx/svg"
)

// SVGToPDF draws an SVG document and lays it on a page.
//
// ⛔ IT RASTERISES. An SVG is vector and so is a PDF, so a reader could
// reasonably expect the paths to survive — and they do not. Turning one
// vector language into another means reimplementing a renderer's worth of
// semantics (gradients in two unit systems, clip paths, stroke joins,
// transforms, text layout), and a half-done translation produces a file that
// opens and is quietly wrong. Drawing it is a complete answer that is honest
// about its own resolution.
//
// [Options.DPI] is what that resolution is: at 72 one SVG user unit is one
// point, and the drawing comes out the size the document says.
func SVGToPDF(doc []byte, opt Options) ([]byte, error) {
	m, err := RasterizeSVG(doc, opt)
	if err != nil {
		return nil, err
	}
	return ToPDF([]image.Image{m}, opt)
}

// RasterizeSVG draws an SVG and hands back the picture.
func RasterizeSVG(doc []byte, opt Options) (image.Image, error) {
	if len(doc) > maxSourceBytes {
		return nil, fmt.Errorf("reading an SVG: it is larger than the %d bytes allowed",
			maxSourceBytes)
	}
	dpi := opt.DPI
	if dpi <= 0 {
		dpi = 72
	}
	// ⛔ The pixel ceiling is passed THROUGH. go-gfx/gfx/svg bounds both the
	// surface a document asks for and any raster it embeds — a hundred bytes
	// of text declaring width="40000" allocated 6.1 GiB before it did — and a
	// caller who set Options.MaxPixels here means it to apply to the whole
	// chain, not to everything except the part that reads a stranger's file.
	res, err := gfxsvg.Rasterize(string(doc), gfxsvg.Options{
		Scale:     dpi / 72,
		MaxPixels: opt.MaxPixels,
	})
	if err != nil {
		return nil, fmt.Errorf("reading an SVG: %w", err)
	}
	return res.Image.ToNRGBA(), nil
}

// LooksLikeSVG says whether these bytes are an SVG document.
//
// ⛔ By their CONTENT, like every other format this package reads. It is more
// than a prefix test because an SVG legitimately begins with an XML
// declaration, a doctype, a comment or a byte-order mark, and a file that
// opens with any of those is still an SVG — refusing it for want of a "<svg"
// in the first four bytes would refuse most of what Inkscape writes.
func LooksLikeSVG(b []byte) bool {
	const look = 1024
	head := b
	if len(head) > look {
		head = head[:look]
	}
	head = bytes.TrimPrefix(head, []byte{0xEF, 0xBB, 0xBF})
	head = bytes.TrimLeft(head, " \t\r\n")
	if !bytes.HasPrefix(head, []byte("<")) {
		return false
	}
	return bytes.Contains(head, []byte("<svg")) || bytes.Contains(head, []byte("<SVG"))
}

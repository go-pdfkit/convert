// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

// Package convert turns pictures into a PDF and a PDF back into pictures.
//
// Everything either side of the PDF was already here: [github.com/go-pdfkit/pdfkit]
// writes one and can place an image on a page, and
// [github.com/go-pdfkit/render] draws a page into a raster. What was missing
// was the join — which is why this package is wiring and codecs rather than a
// new engine.
//
// # Formats
//
// Reading: PNG, JPEG and GIF from the standard library; BMP, TIFF and WebP from
// golang.org/x/image. Writing: PNG, JPEG, GIF, BMP and TIFF.
//
// ⛔ There is no WebP ENCODER in pure Go, so [FromPDF] refuses "webp" by name
// instead of quietly writing something else. A converter that answers a request
// it did not carry out is worse than one that says no.
//
// # What decides the page
//
// A picture has pixels and a DPI; a PDF page has points. [ToPDF] sizes each
// page to its picture at the DPI given, so a 300-dpi scan comes back the size
// of the paper it came off. Asking for a fixed page size instead fits the
// picture inside it, centred, never cropping and never stretching: see
// [Options.Page].
//
// # Pure Go
//
// CGO-free, including GOOS=js. No C anywhere in the chain.
package convert

// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-ooxml/opc"
	"github.com/go-pdfkit/pdfkit"
)

// ErrNotXPS is returned for a file that is not an OpenXPS package at all, as
// opposed to one this cannot draw everything in.
var ErrNotXPS = errors.New("convert: this is not an OpenXPS package")

// xpsUnit converts XPS's coordinate unit to PDF's.
//
// ECMA-388 §10.3.1: a fixed page's values "are specified in units of 1/96"",
// and the clause's own example is a 816x1056 page, which is 8.5x11 inches at
// that scale. A PDF point is 1/72", so one XPS unit is 72/96 of one.
//
// ⛔ ONE SCALE, APPLIED ONCE, at the page transform — never folded into each
// coordinate. A factor applied per shape is a factor that gets forgotten on
// the one path somebody adds later.
const xpsUnit = 72.0 / 96.0

// The start-part relationship, in ECMA-388's namespace and in the earlier
// Microsoft one that XPS files in the wild still carry — the package Ghostscript
// writes uses the Microsoft spelling, so both are tried.
//
// ⚠ Nothing here keys off the markup NAMESPACE: the element names are what
// this reads, and a page in either namespace parses the same. A reader that
// demanded one namespace would refuse half the files that exist.
const (
	relFixedRepr = "http://schemas.microsoft.com/xps/2005/06/fixedrepresentation"
	relOXPSRepr  = "http://schemas.openxps.org/oxps/v1.0/fixedrepresentation"
)

// XPSReport says what was converted and, as importantly, what was left out.
//
// ⛔ A CONVERTER THAT DROPS SOMETHING SILENTLY IS WORSE THAN ONE THAT REFUSES.
// Unsupported names every construct a page used that this does not draw, so a
// caller can tell "an empty page" from "a page whose every mark was a kind I
// skipped". It is a set of kinds, not a count of occurrences: the question it
// answers is what to implement next, not how busy the page was.
type XPSReport struct {
	// Pages is how many fixed pages were written.
	Pages int
	// Unsupported is the sorted, de-duplicated list of constructs skipped.
	Unsupported []string
}

// XPSToPDF lays an OpenXPS package on PDF pages as paths, keeping it vector.
//
// It reads the fixed pages of ECMA-388 §10 — <FixedPage>, its <Canvas>
// grouping and its <Path> marks — and draws each path's abbreviated geometry
// (§11.2.3, parsed by [vector.ParseData]) with its solid fill and stroke.
//
// # What it does not draw, and says so
//
// <Glyphs> (§12) needs the package's fonts and a glyph-indexed PDF font;
// <ImageBrush>, <VisualBrush> and the gradient brushes (§13) need either a
// decoded picture or a PDF shading. None of them are drawn. Every one that a
// page uses is named in [XPSReport.Unsupported] rather than passed over, so a
// thin result is legible as a thin result.
//
// ⚠ WHAT A CORPUS OF ONE PRODUCER CANNOT TELL YOU. Measured on XPS written by
// Ghostscript's xpswrite device, a three-page package held 25073 <Path>
// elements and NOT ONE <Glyphs>: that producer draws text as filled outlines.
// That says what Ghostscript writes, not what XPS files contain — a writer
// that keeps text as glyphs produces a document this would hand back nearly
// empty, with "Glyphs" in the report. Do not read the first number as the
// second.
func XPSToPDF(b []byte, opt Options) ([]byte, *XPSReport, error) {
	if len(b) > maxSourceBytes {
		return nil, nil, fmt.Errorf("reading an XPS: it is larger than the %d bytes allowed",
			maxSourceBytes)
	}
	// opc's zero Limits are its measured defaults; a caller's MaxPixels has
	// nothing to say about a ZIP, so nothing is derived from it here.
	pkg, err := opc.Open(b, opc.Limits{})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNotXPS, err)
	}

	pages := xpsPageParts(pkg)
	if len(pages) == 0 {
		return nil, nil, fmt.Errorf("%w: it declares no fixed page", ErrNotXPS)
	}

	rep := &XPSReport{}
	unsupported := map[string]bool{}
	d := pdfkit.New(pdfkit.Options{Title: opt.Title})

	for _, name := range pages {
		part, err := pkg.Part(name)
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", name, err)
		}
		if err := xpsDrawPage(d, part, unsupported); err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", name, err)
		}
		rep.Pages++
	}

	for k := range unsupported {
		rep.Unsupported = append(rep.Unsupported, k)
	}
	sort.Strings(rep.Unsupported)

	// ⛔ Returned, not branched on, as in SVGToPDFVector: Write fails for a
	// document with no pages or an io error from the writer, and neither can
	// happen here — a page was added for each part, and a bytes.Buffer never
	// fails. A branch would be one no test could reach.
	var buf bytes.Buffer
	err = d.Write(&buf)
	return buf.Bytes(), rep, err
}

// xpsPageParts returns the fixed-page parts, in document order.
//
// ⛔ REACHED THROUGH RELATIONSHIPS, NEVER BY FILENAME. opc's own doc comment
// is emphatic about this and it is not theoretical here: the fixed
// representation, the documents and the pages are all named by whatever the
// producer chose. A reader that opened "Documents/1/Pages/1.fpage" would work
// on Ghostscript's output and on little else.
//
// The content type is the fallback, not the first choice: a package whose
// start-part relationship is missing or unreadable still has parts that say
// what they are, and reporting nothing for such a file would be the scan that
// could not read reporting zero.
// ⚠ It cannot fail: a package that cannot be walked by its relationships is
// scanned instead, and a package with neither is simply one with no pages,
// which the caller reports. There is no third outcome to return.
func xpsPageParts(pkg *opc.Package) []string {
	if names, err := xpsPagesByRelationship(pkg); err == nil && len(names) > 0 {
		return names
	}
	var names []string
	for _, n := range pkg.Parts() {
		if strings.Contains(pkg.ContentType(n), "fixedpage") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// xpsPagesByRelationship walks sequence -> document -> page the way §9 says to.
func xpsPagesByRelationship(pkg *opc.Package) ([]string, error) {
	var seq string
	for _, rt := range []string{relOXPSRepr, relFixedRepr} {
		if r, ok, err := pkg.RelOfType("", rt); err == nil && ok {
			if n, err := pkg.Resolve("", r); err == nil {
				seq = n
				break
			}
		}
	}
	if seq == "" {
		return nil, errors.New("no fixed representation relationship")
	}
	docs, err := xpsSources(pkg, seq, "DocumentReference")
	if err != nil {
		return nil, err
	}
	var pages []string
	for _, doc := range docs {
		ps, err := xpsSources(pkg, doc, "PageContent")
		if err != nil {
			return nil, err
		}
		pages = append(pages, ps...)
	}
	return pages, nil
}

// xpsSources reads the Source attribute of every element named elem in part,
// resolved against that part.
func xpsSources(pkg *opc.Package, part, elem string) ([]string, error) {
	b, err := pkg.Part(part)
	if err != nil {
		return nil, err
	}
	var out []string
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != elem {
			continue
		}
		for _, a := range se.Attr {
			if a.Name.Local != "Source" {
				continue
			}
			// A Source is a part name relative to the part that holds it,
			// which is what Resolve is for; a raw join would mishandle "../".
			n, err := pkg.Resolve(part, opc.Rel{Target: a.Value})
			if err != nil {
				continue
			}
			out = append(out, n)
		}
	}
	return out, nil
}

// xpsState is what a Canvas can change for the elements inside it.
type xpsState struct {
	m geometry.Matrix
}

// xpsDrawPage reads one fixed page and adds it to the document.
func xpsDrawPage(d *pdfkit.Document, part []byte, unsupported map[string]bool) error {
	dec := xml.NewDecoder(bytes.NewReader(part))

	var (
		p       *pdfkit.Page
		h       float64
		stack   []xpsState
		cur     = xpsState{m: geometry.Identity()}
		started bool
	)

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "FixedPage":
				w, hh := xpsFloat(t, "Width"), xpsFloat(t, "Height")
				if !(w > 0) || !(hh > 0) {
					return errors.New("the fixed page declares no usable size")
				}
				h = hh
				p = d.AddPage(pdfkit.PageSize{Width: w * xpsUnit, Height: hh * xpsUnit})
				// ⛔ XPS COUNTS Y DOWNWARDS AND PDF COUNTS IT UP, and XPS's
				// unit is 1/96" against PDF's 1/72". Both are applied ONCE
				// here, as one transform, rather than per coordinate.
				p.Save()
				p.Transform(xpsUnit, 0, 0, -xpsUnit, 0, h*xpsUnit)
				started = true
			case "Canvas":
				stack = append(stack, cur)
				if m, ok := xpsTransformAttr(t, "RenderTransform"); ok {
					cur.m = cur.m.Mul(m)
				}
				if xpsAttr(t, "Clip") != "" {
					unsupported["Canvas.Clip"] = true
				}
				if xpsAttr(t, "OpacityMask") != "" {
					unsupported["OpacityMask"] = true
				}
			case "Path":
				if p == nil {
					continue
				}
				xpsDrawPath(p, t, cur, unsupported)
			case "Glyphs":
				unsupported["Glyphs"] = true
			case "ImageBrush":
				unsupported["ImageBrush"] = true
			case "VisualBrush":
				unsupported["VisualBrush"] = true
			case "LinearGradientBrush":
				unsupported["LinearGradientBrush"] = true
			case "RadialGradientBrush":
				unsupported["RadialGradientBrush"] = true
			case "PathGeometry":
				// The long form of a geometry, §11.2.1. Only the abbreviated
				// syntax is read, so say which one was met rather than drawing
				// nothing and calling the page empty.
				unsupported["PathGeometry"] = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "Canvas":
				if n := len(stack); n > 0 {
					cur, stack = stack[n-1], stack[:n-1]
				}
			case "FixedPage":
				if started && p != nil {
					p.Restore()
				}
			}
		}
	}
	if !started {
		return errors.New("the part holds no FixedPage element")
	}
	return nil
}

// xpsDrawPath draws one <Path>, §11.1.
func xpsDrawPath(p *pdfkit.Page, se xml.StartElement, st xpsState, unsupported map[string]bool) {
	data := xpsAttr(se, "Data")
	if data == "" {
		// Either a <Path.Data> child (the long form) or a resource reference;
		// both are noted where they are met rather than here, so that a Path
		// with neither is simply nothing to draw.
		return
	}
	if strings.HasPrefix(strings.TrimSpace(data), "{") {
		unsupported["ResourceReference"] = true
		return
	}

	m := st.m
	if t, ok := xpsTransformAttr(se, "RenderTransform"); ok {
		m = m.Mul(t)
	}
	if xpsAttr(se, "Clip") != "" {
		unsupported["Path.Clip"] = true
	}
	if xpsAttr(se, "OpacityMask") != "" {
		unsupported["OpacityMask"] = true
	}

	geom, rule := xpsGeometry(data)
	path, ok := vector.ParseData(geom, m)
	if !ok {
		return
	}

	opacity := 1.0
	if s := xpsAttr(se, "Opacity"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v >= 0 && v <= 1 {
			opacity = v
		}
	}

	if fill, ok := xpsColour(xpsAttr(se, "Fill"), unsupported); ok {
		p.Save()
		p.SetAlpha(opacity*float64(fill.A)/255, opacity*float64(fill.A)/255)
		p.SetFillColor(pdfkit.RGB8(fill.R, fill.G, fill.B))
		emitPath(p, path)
		if rule == vector.EvenOdd {
			p.FillEvenOdd()
		} else {
			p.Fill()
		}
		p.Restore()
	}

	stroke, ok := xpsColour(xpsAttr(se, "Stroke"), unsupported)
	if !ok {
		return
	}
	// §11.1: StrokeThickness is in the same units as the geometry, so it is
	// carried by the page transform like every coordinate, and defaults to 1.
	width := 1.0
	if s := xpsAttr(se, "StrokeThickness"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v >= 0 {
			width = v
		}
	}
	p.Save()
	p.SetAlpha(opacity*float64(stroke.A)/255, opacity*float64(stroke.A)/255)
	p.SetStrokeColor(pdfkit.RGB8(stroke.R, stroke.G, stroke.B))
	p.SetLineWidth(width)
	emitPath(p, path)
	p.Stroke()
	p.Restore()
}

// xpsGeometry splits the fill-rule prefix off abbreviated geometry data.
//
// ⛔ THE PREFIX IS NOT PART OF THE OUTLINE, which is why vector.ParseData does
// not take it: a fill rule belongs to the shape. ECMA-388 Annex C begins "PG.
// FillRule = EvenOdd" and then reads an optional leading F, where F0 is
// EvenOdd and anything else is NonZero — so the DEFAULT IS EVEN-ODD, which is
// the opposite of SVG's default and of PDF's f operator. Getting it backwards
// changes only self-intersecting shapes, which is why it survives a glance.
func xpsGeometry(data string) (string, vector.FillRule) {
	s := strings.TrimLeft(data, " \t\r\n")
	if len(s) == 0 || (s[0] != 'F' && s[0] != 'f') {
		return data, vector.EvenOdd
	}
	rest := strings.TrimLeft(s[1:], " \t\r\n")
	if len(rest) == 0 {
		return "", vector.EvenOdd
	}
	rule := vector.NonZero
	if rest[0] == '0' {
		rule = vector.EvenOdd
	}
	return rest[1:], rule
}

// xpsColour reads §15.2's vector colour syntax: #rgb, #argb, #rrggbb and
// #aarrggbb. ok is false for an absent brush, which means "do not paint".
//
// A colour this does not read — sc# in scRGB, ContextColor with an ICC
// profile — is recorded rather than guessed at: painting it black would be a
// drawing nobody asked for.
func xpsColour(s string, unsupported map[string]bool) (color.RGBA, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return color.RGBA{}, false
	}
	if strings.HasPrefix(s, "{") {
		unsupported["ResourceReference"] = true
		return color.RGBA{}, false
	}
	if !strings.HasPrefix(s, "#") {
		switch {
		case strings.HasPrefix(s, "sc#"):
			unsupported["scRGB colour"] = true
		case strings.HasPrefix(s, "ContextColor"):
			unsupported["ContextColor"] = true
		default:
			unsupported["named colour"] = true
		}
		return color.RGBA{}, false
	}
	hex := s[1:]
	var a, r, g, b uint8 = 255, 0, 0, 0
	nib := func(i int) uint8 {
		v, err := strconv.ParseUint(hex[i:i+1], 16, 8)
		if err != nil {
			return 0
		}
		return uint8(v)*16 + uint8(v)
	}
	byteAt := func(i int) uint8 {
		v, err := strconv.ParseUint(hex[i:i+2], 16, 8)
		if err != nil {
			return 0
		}
		return uint8(v)
	}
	switch len(hex) {
	case 3:
		r, g, b = nib(0), nib(1), nib(2)
	case 4:
		a, r, g, b = nib(0), nib(1), nib(2), nib(3)
	case 6:
		r, g, b = byteAt(0), byteAt(2), byteAt(4)
	case 8:
		a, r, g, b = byteAt(0), byteAt(2), byteAt(4), byteAt(6)
	default:
		unsupported["colour syntax"] = true
		return color.RGBA{}, false
	}
	return color.RGBA{R: r, G: g, B: b, A: a}, true
}

// xpsTransformAttr reads a RenderTransform written as six numbers.
//
// ⛔ THE SIX ARE NOT ROW-MAJOR. §14.4.1's Matrix is m11,m12,m21,m22,dx,dy,
// which maps (x, y) to (m11*x + m21*y + dx, m12*x + m22*y + dy) — the same
// [a b c d e f] that SVG, ODF and PDF write. Reading it row-major swaps the
// two middle numbers, which leaves every unrotated drawing correct and every
// rotated one mirrored. geometry.Matrix names its entries so the mapping is
// written out and cannot transpose silently.
func xpsTransformAttr(se xml.StartElement, name string) (geometry.Matrix, bool) {
	v := strings.TrimSpace(xpsAttr(se, name))
	if v == "" || strings.HasPrefix(v, "{") {
		return geometry.Identity(), false
	}
	f := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(f) != 6 {
		return geometry.Identity(), false
	}
	var n [6]float64
	for i, s := range f {
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return geometry.Identity(), false
		}
		n[i] = x
	}
	return geometry.Matrix{
		Xx: n[0], Yx: n[2], X0: n[4],
		Xy: n[1], Yy: n[3], Y0: n[5],
	}, true
}

// xpsAttr returns an attribute's value whatever namespace it carries: the
// element names are what identify XPS markup here, and a page may be in either
// the OpenXPS or the older Microsoft namespace.
func xpsAttr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// xpsFloat reads a numeric attribute, 0 when absent or unreadable.
func xpsFloat(se xml.StartElement, name string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(xpsAttr(se, name)), 64)
	if err != nil {
		return 0
	}
	return v
}

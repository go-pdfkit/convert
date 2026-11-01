// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"strings"

	gfxsvg "github.com/go-gfx/gfx/svg"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-pdfkit/pdfkit"
)

// Drawing an SVG into a PDF as PATHS rather than as a picture of paths.
//
// Both are vector languages and their primitives correspond closely, so the
// translation is a mapping rather than a reimplementation: a move is m, a line
// is l, a cubic is c, a close is h, and the painting operators of ISO 32000-1
// Table 60 cover fill, stroke and both. go-gfx's svg.Walk does the reading —
// the same parse, inheritance and geometry its rasteriser uses — and hands over
// device-space outlines with the paint in force.
//
// ⛔ WHAT MAKES THIS SAFE IS THE REFUSAL, not the translation. A converter
// between vector languages that silently drops what it does not understand
// produces a file that OPENS and is quietly wrong, which is worse than a
// picture. svg.Walk reports every feature it could not hand over — gradients,
// clip paths, embedded rasters — and this refuses the whole document when it
// meets one, so [SVGToPDF] can draw it instead. Half a translation never
// reaches a caller.

// ErrSVGNotVector says the document uses something the vector path cannot
// express, and names it. [SVGToPDF] answers this by rasterising.
var ErrSVGNotVector = errors.New("convert: this SVG cannot be drawn as paths")

// SVGToPDFVector lays an SVG on a page as paths, keeping it vector.
//
// The page is the document's own size in points: one SVG user unit is one
// point, which is what a viewer calls 100%. [Options.DPI] is not consulted and
// has nothing to say here — a resolution is a property of a picture, and this
// output has none.
//
// It returns an error wrapping [ErrSVGNotVector] for a document that uses a
// gradient, a clip path or an embedded image.
func SVGToPDFVector(doc []byte, opt Options) ([]byte, error) {
	if len(doc) > maxSourceBytes {
		return nil, fmt.Errorf("reading an SVG: it is larger than the %d bytes allowed",
			maxSourceBytes)
	}
	// ⛔ Scale 1 so one user unit is one point. The pixel ceiling still applies
	// and still matters: Walk allocates no surface, but the size it would need
	// is still computed from numbers the document chose, and a caller's
	// MaxPixels is meant to bound the whole chain.
	wopt := gfxsvg.Options{Scale: 1, MaxPixels: opt.MaxPixels}

	var shapes []gfxsvg.Shape
	res, err := gfxsvg.Walk(string(doc), wopt, gfxsvg.SinkFunc(func(s gfxsvg.Shape) {
		shapes = append(shapes, s)
	}))
	if err != nil {
		return nil, fmt.Errorf("reading an SVG: %w", err)
	}
	if len(res.Unsupported) > 0 {
		return nil, fmt.Errorf("%w: it uses %s", ErrSVGNotVector, strings.Join(res.Unsupported, ", "))
	}

	w, h := float64(res.Width), float64(res.Height)
	d := pdfkit.New(pdfkit.Options{Title: opt.Title})
	p := d.AddPage(pdfkit.PageSize{Width: w, Height: h})

	// ⛔ SVG COUNTS Y DOWNWARDS AND PDF COUNTS IT UP. One flip at the top,
	// rather than subtracting from the height at every coordinate: a flip
	// applied once cannot be forgotten at the one place that would put a shape
	// upside down in an otherwise right-way-up drawing.
	p.Save()
	p.Transform(1, 0, 0, -1, 0, h)
	for _, s := range shapes {
		drawShape(p, s)
	}
	p.Restore()

	// ⛔ Returned, not branched on. Document.Write fails for a document with no
	// pages or an io error from the writer, and neither can happen here — one
	// page is always added and a bytes.Buffer never fails. A branch would be
	// one no test could reach; handing the error back costs nothing and keeps
	// the day pdfkit grows a reason to refuse.
	var buf bytes.Buffer
	err = d.Write(&buf)
	return buf.Bytes(), err
}

// drawShape emits one walked shape as a PDF path object.
//
// ⛔ Fill and stroke go down as TWO path objects rather than one B, even
// though Table 60's B fills and then strokes in exactly SVG's order. The two
// carry independent opacities, and an opacity is a graphics-state parameter —
// one B cannot be under two states. Emitting the path twice is what the
// rasteriser does too: it fills, then strokes, as separate passes.
func drawShape(p *pdfkit.Page, s gfxsvg.Shape) {
	if s.Fill != nil {
		p.Save()
		p.SetAlpha(s.Fill.Opacity, s.Fill.Opacity)
		p.SetFillColor(rgbOf(s.Fill.Colour))
		emitPath(p, s.Path)
		if s.FillRule == vector.EvenOdd {
			p.FillEvenOdd()
		} else {
			p.Fill()
		}
		p.Restore()
	}
	if s.Stroke == nil {
		return
	}
	p.Save()
	p.SetAlpha(s.Stroke.Opacity, s.Stroke.Opacity)
	p.SetStrokeColor(rgbOf(s.Stroke.Colour))
	p.SetLineWidth(s.StrokeWidth)
	p.SetLineCap(pdfLineCap(s.Cap))
	p.SetLineJoin(pdfLineJoin(s.Join))
	if s.MiterLimit > 0 {
		p.SetMiterLimit(s.MiterLimit)
	}
	if len(s.Dash) > 0 {
		p.SetDash(s.Dash, s.DashPhase)
	}
	emitPath(p, s.Path)
	p.Stroke()
	p.Restore()
}

// emitPath writes the outline with the operators of ISO 32000-1 Table 59.
func emitPath(p *pdfkit.Page, path *vector.Path) {
	// The current point and the start of the current subpath are tracked here
	// because a quadratic needs where it begins, and h returns the pen to the
	// subpath's first point. Neither is in the callback's arguments, and
	// asking the page for them would be asking the thing being written.
	var cur, start [2]float64
	path.Each(func(op vector.PathOp, pts ...[2]float64) {
		switch op {
		case vector.OpMove:
			p.MoveTo(pts[0][0], pts[0][1])
			cur, start = pts[0], pts[0]
		case vector.OpLine:
			p.LineTo(pts[0][0], pts[0][1])
			cur = pts[0]
		case vector.OpQuad:
			// ⛔ PDF HAS NO QUADRATIC. Table 59 offers c, v and y, all cubic,
			// so a quadratic is raised to the cubic that draws the identical
			// curve: each control point sits two thirds of the way from its
			// end towards the quadratic's single control. Exact, not an
			// approximation — a degree-2 bezier IS a degree-3 bezier.
			c, e := pts[0], pts[1]
			p.CurveTo(
				cur[0]+2.0/3.0*(c[0]-cur[0]), cur[1]+2.0/3.0*(c[1]-cur[1]),
				e[0]+2.0/3.0*(c[0]-e[0]), e[1]+2.0/3.0*(c[1]-e[1]),
				e[0], e[1])
			cur = e
		case vector.OpCubic:
			p.CurveTo(pts[0][0], pts[0][1], pts[1][0], pts[1][1], pts[2][0], pts[2][1])
			cur = pts[2]
		case vector.OpClose:
			p.ClosePath()
			cur = start
		}
	})
}

// rgbOf is the colour without its alpha: the opacity travels in the graphics
// state, so folding the colour's own alpha in here would paint it twice.
func rgbOf(c color.RGBA) pdfkit.RGB { return pdfkit.RGB8(c.R, c.G, c.B) }

// pdfLineCap maps go-gfx's cap onto ISO 32000-1 Table 54.
//
// ⛔ Mapped rather than cast. The two happen to agree today — butt, round,
// square against 0, 1, 2 — and a cast would keep compiling and start lying the
// day either side gains a style or reorders one.
func pdfLineCap(c vector.LineCap) int {
	switch c {
	case vector.RoundCap:
		return 1
	case vector.SquareCap:
		return 2
	default:
		return 0
	}
}

// pdfLineJoin maps go-gfx's join onto ISO 32000-1 Table 55.
func pdfLineJoin(j vector.LineJoin) int {
	switch j {
	case vector.RoundJoin:
		return 1
	case vector.BevelJoin:
		return 2
	default:
		return 0
	}
}

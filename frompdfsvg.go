// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/go-pdfkit/render"
)

// ⛔ Variables, for the reason the archive ceilings are: a limit no test can
// reach is a claim. A test lowers them; nothing else should.
var (
	// MaxSVGPages is how many pages one run may write. A PDF can declare any
	// number of them, and every one costs up to ten seconds and 64 MiB of
	// interpreter, so the count is refused BEFORE the first page is drawn and the
	// message names -pages as the way to take fewer.
	MaxSVGPages = 1000

	// MaxSVGBytes is how much SVG all the pages together may come to, and so
	// bounds the zip too: the archive holds exactly these bytes, deflated, and
	// deflate of text does not grow it past this. One page under render's own
	// limit says nothing about a thousand of them.
	MaxSVGBytes = 256 << 20

	// MaxSVGRunTime is how long the whole run may interpret pages. A page may
	// take MaxDuration, which bounds one page; this bounds the sum.
	MaxSVGRunTime = 5 * time.Minute
)

// Seams, so a test can say what a page does without a PDF that provokes it.
var (
	pageSVG = render.PageSVG
	now     = time.Now
)

// SVGOptions says which pages become SVG, and how much of the machine a file
// may use to get there.
type SVGOptions struct {
	// Pages selects pages, one-based, in the order given. Nil means every page.
	// A page named twice is refused: the archive names its entries after the
	// page, and two entries of one name read differently in every unzipper.
	Pages []int

	// Password opens an encrypted file.
	Password string

	// MaxBytes bounds ONE page's SVG; zero is [DefaultSVGPageBytes], 64 MiB. A
	// page is never granted more than the run has left of [MaxSVGBytes], which
	// bounds the total whatever this says.
	MaxBytes int

	// MaxDuration bounds one page; zero is [DefaultSVGPageTime], ten seconds. A
	// page is never granted more than the run has left of [MaxSVGRunTime]. See
	// [StreamSVG] on what the limit is worth while render#142 is open.
	MaxDuration time.Duration

	// RasterScale is the pixels per point of what SVG cannot say as a shape; zero
	// is render's two.
	RasterScale float64
}

// SVGPage is one page written as SVG.
type SVGPage struct {
	Number int // one-based, as the file numbers it
	Data   []byte
}

// DefaultSVGPageBytes and DefaultSVGPageTime are what ONE page may take when
// the options say nothing: render's own defaults, spelled here because the run's
// totals must never stand in for them.
const (
	DefaultSVGPageBytes = 64 << 20
	DefaultSVGPageTime  = 10 * time.Second
)

// StreamSVG draws the selected pages one at a time and hands each to emit with
// its number and how many pages were selected, in the order given. Its peak
// memory is ONE page's SVG and what drawing it takes, whatever the page count;
// emit owns the slice and may keep it.
//
// ⛔ If emit returns an error the run stops with it, unwrapped. A caller that
// writes the pages must therefore treat any error as "discard what was
// written": the policy is all or nothing, like [PDFToSVG].
//
// ⛔ The run's time is checked BETWEEN pages and handed to each page as its
// limit, so a single page inherits whatever render's deadline does: while
// go-pdfkit/render#142 is open that deadline can be overrun many times within
// one page, and the ten seconds a page gets here are a request, not a promise.
//
// The policy is the one FromPDF has: the first page that cannot be written
// stops the run, the error NAMES the page, and nothing is returned. A zip with
// page 4 of 9 missing opens, counts and looks complete, which is the worst
// shape a failure can take. Both [render.ErrSVGTooDeep] (issue render#143 tracks
// a raster fallback) and [render.ErrTimedOut] stay reachable through
// [errors.Is].
//
// The input is untrusted. Everything is bounded before it is spent: the page
// count before the first page, each page's output by render, the total by
// [MaxSVGBytes] (handed to render as the page's limit, so it is never allocated
// and THEN found too large), and the time by [MaxSVGRunTime].
func StreamSVG(pdf []byte, opt SVGOptions, emit func(page, of int, svg []byte) error) error {
	doc, err := open(pdf, opt.Password)
	if err != nil {
		return fmt.Errorf("opening the PDF: %w", err)
	}
	total := doc.PageCount()
	if total == 0 {
		return fmt.Errorf("the PDF opened and holds no pages")
	}
	want := opt.Pages
	if len(want) == 0 {
		// ⛔ Checked BEFORE the list is made: the count is the file's own claim,
		// and a slice of five million ints is built from nothing else.
		if total > MaxSVGPages {
			return fmt.Errorf("the PDF has %d pages, past the %d allowed in one run: choose with -pages",
				total, MaxSVGPages)
		}
		want = make([]int, total)
		for i := range want {
			want[i] = i + 1
		}
	}
	if len(want) > MaxSVGPages {
		return fmt.Errorf("%d pages asked for, past the %d allowed in one run: choose with -pages",
			len(want), MaxSVGPages)
	}
	seen := make(map[int]bool, len(want))
	for _, n := range want {
		if n < 1 || n > total {
			return fmt.Errorf("page %d: this file has %d", n, total)
		}
		if seen[n] {
			return fmt.Errorf("page %d is selected twice", n)
		}
		seen[n] = true
	}

	start := now()
	budget := MaxSVGBytes
	for _, n := range want {
		left := MaxSVGRunTime - now().Sub(start)
		if left <= 0 {
			return fmt.Errorf("page %d: the run has used its %s and stops here: choose with -pages",
				n, MaxSVGRunTime)
		}
		if budget <= 0 {
			return fmt.Errorf("page %d: the pages so far fill the %d bytes allowed in one run: choose with -pages",
				n, MaxSVGBytes)
		}
		// ⛔ A page's share is the SMALLER of what it was granted (the option, or
		// render's own default) and what the run has left. It used to be the
		// whole run's budget when the option was zero, so one page could take 256
		// MiB and five minutes, with a heap ten times its SVG.
		perPage := opt.MaxBytes
		if perPage <= 0 {
			perPage = DefaultSVGPageBytes
		}
		if perPage > budget {
			perPage = budget
		}
		dur := opt.MaxDuration
		if dur <= 0 {
			dur = DefaultSVGPageTime
		}
		if dur > left {
			dur = left
		}
		var buf bytes.Buffer
		if err := pageSVG(doc, n, &buf, render.SVGOptions{
			MaxBytes: perPage, RasterScale: opt.RasterScale, MaxDuration: dur,
		}); err != nil {
			return svgPageError(n, err)
		}
		// ⛔ render counts the limit against the page's marks, not against the
		// finished document: the wrapper around them is a hundred bytes or so
		// that the limit does not see, and a thousand pages of it is not
		// nothing. The ceiling here is on what is actually kept.
		if buf.Len() > perPage {
			return svgPageError(n, render.ErrSVGTooLarge)
		}
		budget -= buf.Len()
		if err := emit(n, len(want), buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// PDFToSVG writes the pages of a PDF as SVG, all or nothing, holding every
// page in memory: up to [MaxSVGBytes] of it. A caller that writes the pages
// somewhere should use [StreamSVG], whose peak is one page.
func PDFToSVG(pdf []byte, opt SVGOptions) ([]SVGPage, error) {
	var out []SVGPage
	err := StreamSVG(pdf, opt, func(n, _ int, svg []byte) error {
		out = append(out, SVGPage{Number: n, Data: svg})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// svgPageError says WHICH page and why, in words, keeping the cause for
// errors.Is.
func svgPageError(n int, err error) error {
	switch {
	case errors.Is(err, render.ErrSVGTooDeep):
		return fmt.Errorf("page %d: its clips nest deeper than an SVG can hold: %w", n, err)
	case errors.Is(err, render.ErrTimedOut):
		return fmt.Errorf("page %d: it was still being drawn when its time ran out: %w", n, err)
	case errors.Is(err, render.ErrSVGTooLarge):
		return fmt.Errorf("page %d: it would come out larger than the limit: %w", n, err)
	}
	return fmt.Errorf("page %d: %w", n, err)
}

// SVGEntryName is the name of a page inside the archive. It is built from the
// page NUMBER and nothing else: no byte of the PDF reaches an entry name, so a
// file cannot ask for "../../x" or an absolute path in an unzipper. Not padded,
// as BentoPDF's is not.
func SVGEntryName(n int) string { return fmt.Sprintf("page_%d.svg", n) }

// SVGArchiveWriter builds the ZIP a page at a time, so that the pages need not
// be held together: the peak is the page being added and the zip writer's state.
type SVGArchiveWriter struct {
	zw   archiveWriter
	seen map[int]bool
	sum  int
}

// NewSVGArchive starts a ZIP of page_N.svg entries on w.
func NewSVGArchive(w io.Writer) *SVGArchiveWriter {
	return &SVGArchiveWriter{zw: newArchive(w), seen: map[int]bool{}}
}

// Add writes page n. It refuses a number already added (two entries of one name
// read differently in every unzipper) and a total above [MaxSVGBytes], BEFORE
// writing anything of the page.
func (a *SVGArchiveWriter) Add(n int, data []byte) error {
	if a.seen[n] {
		return fmt.Errorf("page %d is in the archive twice", n)
	}
	// Compared as a subtraction: a.sum+len(data) can wrap on a 32-bit int for a
	// slice near 2 GiB, and a wrapped sum is negative, so it would pass.
	if len(data) > MaxSVGBytes-a.sum {
		return fmt.Errorf("the pages come to more than the %d bytes allowed", MaxSVGBytes)
	}
	a.seen[n] = true
	a.sum += len(data)
	name := SVGEntryName(n)
	f, err := a.zw.Create(name)
	if err == nil {
		_, err = f.Write(data)
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

// Close finishes the archive. An archive with no page is refused: an empty one
// is not a set of pictures.
func (a *SVGArchiveWriter) Close() error {
	if len(a.seen) == 0 {
		return fmt.Errorf("no pages: an empty archive is not a set of pictures")
	}
	return a.zw.Close()
}

// SVGArchive writes the pages into a ZIP as page_N.svg.
func SVGArchive(w io.Writer, ps []SVGPage) error {
	a := NewSVGArchive(w)
	for _, p := range ps {
		if err := a.Add(p.Number, p.Data); err != nil {
			return err
		}
	}
	return a.Close()
}

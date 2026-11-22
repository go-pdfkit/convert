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

	// MaxBytes bounds ONE page's SVG; zero leaves render's default of 64 MiB.
	// The total is bounded by [MaxSVGBytes] whatever this says.
	MaxBytes int

	// MaxDuration bounds one page; zero is render's ten seconds.
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

// PDFToSVG writes the pages of a PDF as SVG, all or nothing.
//
// ⛔ The policy is the one FromPDF has: the first page that cannot be written
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
func PDFToSVG(pdf []byte, opt SVGOptions) ([]SVGPage, error) {
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
		// ⛔ Checked BEFORE the list is made: the count is the file's own claim,
		// and a slice of five million ints is built from nothing else.
		if total > MaxSVGPages {
			return nil, fmt.Errorf("the PDF has %d pages, past the %d allowed in one run: choose with -pages",
				total, MaxSVGPages)
		}
		want = make([]int, total)
		for i := range want {
			want[i] = i + 1
		}
	}
	if len(want) > MaxSVGPages {
		return nil, fmt.Errorf("%d pages asked for, past the %d allowed in one run: choose with -pages",
			len(want), MaxSVGPages)
	}
	seen := make(map[int]bool, len(want))
	for _, n := range want {
		if n < 1 || n > total {
			return nil, fmt.Errorf("page %d: this file has %d", n, total)
		}
		if seen[n] {
			return nil, fmt.Errorf("page %d is selected twice", n)
		}
		seen[n] = true
	}

	start := now()
	budget := MaxSVGBytes
	out := make([]SVGPage, 0, len(want))
	for _, n := range want {
		left := MaxSVGRunTime - now().Sub(start)
		if left <= 0 {
			return nil, fmt.Errorf("page %d: the run has used its %s and stops here: choose with -pages",
				n, MaxSVGRunTime)
		}
		if budget <= 0 {
			return nil, fmt.Errorf("page %d: the pages so far fill the %d bytes allowed in one run: choose with -pages",
				n, MaxSVGBytes)
		}
		perPage := opt.MaxBytes
		if perPage <= 0 || perPage > budget {
			perPage = budget
		}
		dur := opt.MaxDuration
		if dur <= 0 || dur > left {
			dur = left
		}
		var buf bytes.Buffer
		if err := pageSVG(doc, n, &buf, render.SVGOptions{
			MaxBytes: perPage, RasterScale: opt.RasterScale, MaxDuration: dur,
		}); err != nil {
			return nil, svgPageError(n, err)
		}
		// ⛔ render counts the limit against the page's marks, not against the
		// finished document: the wrapper around them is a hundred bytes or so
		// that the limit does not see, and a thousand pages of it is not
		// nothing. The ceiling here is on what is actually kept.
		if buf.Len() > perPage {
			return nil, svgPageError(n, render.ErrSVGTooLarge)
		}
		budget -= buf.Len()
		out = append(out, SVGPage{Number: n, Data: buf.Bytes()})
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

// SVGArchive writes the pages into a ZIP as page_N.svg.
func SVGArchive(w io.Writer, ps []SVGPage) error {
	if len(ps) == 0 {
		return fmt.Errorf("no pages: an empty archive is not a set of pictures")
	}
	var sum int
	for _, p := range ps {
		sum += len(p.Data)
		if sum > MaxSVGBytes {
			return fmt.Errorf("the pages come to more than the %d bytes allowed", MaxSVGBytes)
		}
	}
	zw := newArchive(w)
	for _, p := range ps {
		name := SVGEntryName(p.Number)
		f, err := zw.Create(name)
		if err == nil {
			_, err = f.Write(p.Data)
		}
		if err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return zw.Close()
}

// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"flag"
	"fmt"
	"image/color"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/go-pdfkit/convert"
)

func redraw(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("redraw", flag.ContinueOnError)
	fs.SetOutput(errw)
	dpi := fs.Float64("dpi", 150, "dots per inch to draw at, and the size the pages come back")
	pages := fs.String("pages", "", "which pages, e.g. 1,3,5-9; empty means all")
	password := fs.String("password", "", "password for an encrypted file")
	title := fs.String("title", "", "title for the new document")
	grey := fs.Bool("greyscale", false, "drop the colour")
	invert := fs.Bool("invert", false, "turn light into dark")
	bright := fs.Float64("brightness", 0, "-1 (black) to 1 (white)")
	contrast := fs.Float64("contrast", 1, "1 leaves it alone, 2 doubles it, 0.5 halves it")
	bg := fs.String("background", "", "paint this under the page, e.g. #ffffff")
	scanner := fs.Bool("scanner", false, "make it look like it went through a flatbed")
	skew := fs.Float64("skew", 0.4, "scanner: degrees off square")
	noise := fs.Float64("noise", 0.12, "scanner: grain, 0 to 1")
	fade := fs.Float64("fade", 0.15, "scanner: how worn the lamp is, 0 to 1")
	seed := fs.Uint64("seed", 0, "scanner: grain seed; zero is a FIXED seed, not a random one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("redraw takes an input and an output: pdfconv redraw <in.pdf> <out.pdf>")
	}

	opt := convert.RedrawOptions{
		RasterOptions: convert.RasterOptions{DPI: *dpi, Password: *password},
		Title:         *title,
		Greyscale:     *grey,
		Invert:        *invert,
		Brightness:    *bright,
		Contrast:      *contrast,
	}
	var err error
	if opt.Pages, err = parseRange(*pages); err != nil {
		return err
	}
	if *bg != "" {
		c, err := parseColour(*bg)
		if err != nil {
			return err
		}
		opt.Background = &c
	}
	if *scanner {
		opt.Scanner = &convert.ScannerEffect{
			Skew: *skew, Noise: *noise, Fade: *fade, Seed: *seed,
		}
	}

	// ⛔ Said before the work, not after. Somebody who meant `pdfops rotate`
	// and typed this gets a file with no text in it and no warning anywhere,
	// and will not find out until they try to search it.
	if nothingAsked(opt) {
		fmt.Fprintln(errw, "pdfconv: redraw with no change asked for — the pages will be "+
			"rasterised and will come back WITHOUT TEXT. That is `rasterize-pdf`; if you "+
			"wanted to keep the text, pdfops is the tool.")
	}

	src, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	pdf, err := convert.Redraw(src, opt)
	if err != nil {
		return fmt.Errorf("%s: %w", fs.Arg(0), err)
	}
	if err := os.WriteFile(fs.Arg(1), pdf, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d bytes (rasterised: the text is now pixels)\n",
		fs.Arg(1), len(pdf))
	return nil
}

// nothingAsked says whether every transform is at its no-op value.
func nothingAsked(o convert.RedrawOptions) bool {
	return !o.Greyscale && !o.Invert && o.Brightness == 0 &&
		(o.Contrast == 0 || o.Contrast == 1) && o.Background == nil && o.Scanner == nil
}

// parseColour reads #rgb, #rrggbb or #rrggbbaa.
//
// ⛔ It refuses anything else rather than falling back to black. A typo in a
// colour would otherwise paint every page black under the content, and on a
// page whose content is opaque that is invisible until the one page that is
// not.
func parseColour(s string) (color.RGBA, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "#")
	switch len(t) {
	case 3:
		t = string([]byte{t[0], t[0], t[1], t[1], t[2], t[2]}) + "ff"
	case 6:
		t += "ff"
	case 8:
	default:
		return color.RGBA{}, fmt.Errorf("a colour is #rgb, #rrggbb or #rrggbbaa, not %q", s)
	}
	n, err := strconv.ParseUint(t, 16, 64)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("%q is not a colour: %w", s, err)
	}
	return color.RGBA{
		R: uint8(n >> 24), G: uint8(n >> 16), B: uint8(n >> 8), A: uint8(n),
	}, nil
}

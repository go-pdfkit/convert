// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pdfkit/convert"
)

// svgArchive is a seam: the zip's own refusal is unreachable after a run that
// already stayed under the same ceiling, and an unreachable branch is a claim
// nobody has checked.
var svgArchive = convert.SVGArchive

// toSVG writes the pages of a PDF as SVG, named the way BentoPDF names them: a
// single page is name.svg, several are name_svg.zip holding page_N.svg.
//
// ⛔ All or nothing, like from-pdf and to-archive: the first page that cannot
// be written stops the run, the error names the page, and no file is left
// behind. The output is built in memory and written once.
func toSVG(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("to-svg", flag.ContinueOnError)
	fs.SetOutput(errw)
	pages := fs.String("pages", "", "which pages, e.g. 1,3,5-9; empty means all")
	password := fs.String("password", "", "password for an encrypted file")
	outDir := fs.String("o", ".", "the directory to write into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("to-svg takes exactly one PDF: pdfconv to-svg [-pages 1-3] [-o dir] <in.pdf>")
	}
	want, err := parseRange(*pages)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	ps, err := convert.PDFToSVG(src, convert.SVGOptions{Pages: want, Password: *password})
	if err != nil {
		return fmt.Errorf("%s: %w", fs.Arg(0), err)
	}

	base := strings.TrimSuffix(filepath.Base(fs.Arg(0)), filepath.Ext(fs.Arg(0)))
	name := base + ".svg"
	data := ps[0].Data
	if len(ps) > 1 {
		name = base + "_svg.zip"
		var buf bytes.Buffer
		if err := svgArchive(&buf, ps); err != nil {
			return fmt.Errorf("%s: %w", fs.Arg(0), err)
		}
		data = buf.Bytes()
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(*outDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d page(s), %d bytes\n", path, len(ps), len(data))
	return nil
}

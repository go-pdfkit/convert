// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

// pdfconv turns pictures into a PDF and a PDF back into pictures.
package main

import (
	"flag"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-pdfkit/convert"
)

// osExit is a variable so the tests can reach the exit path without ending
// the test binary — the same shape cmd/pdfops uses.
var osExit = os.Exit

func main() { osExit(mainish(os.Args[1:], os.Stdout, os.Stderr)) }

// mainish is main with its streams and its exit CODE handed back, so that a
// test can look at both.
func mainish(args []string, out, errw io.Writer) int {
	if err := runTo(args, out, errw); err != nil {
		fmt.Fprintln(errw, "pdfconv:", err)
		return 1
	}
	return 0
}

// run is runTo over the real streams, for the tests that only care whether it
// refused and why.
func run(args []string) error { return runTo(args, os.Stdout, os.Stderr) }

func runTo(args []string, out, errw io.Writer) error {
	if len(args) == 0 {
		usage(errw)
		return fmt.Errorf("a command is required")
	}
	switch args[0] {
	case "to-pdf":
		return toPDF(args[1:], out, errw)
	case "from-pdf":
		return fromPDF(args[1:], out, errw)
	case "formats":
		fmt.Fprintln(out, "read :", strings.Join([]string{"png", "jpeg", "gif", "bmp", "tiff", "webp"}, ", "))
		fmt.Fprintln(out, "write:", strings.Join(convert.Encoders(), ", "))
		fmt.Fprintln(out, "paper:", strings.Join(convert.PageSizes(), ", "),
			"(any with -landscape, or WIDTHxHEIGHT in points)")
		return nil
	case "-h", "--help", "help":
		usage(out)
		return nil
	default:
		usage(errw)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `
pdfconv — pictures to a PDF, and a PDF back to pictures.

  to-pdf    lay each picture on a page
              pdfconv to-pdf [-dpi n] [-page a4] [-margin pt] [-title s] -o <out.pdf> <in> [in …]
  from-pdf  draw each page and write it out
              pdfconv from-pdf [-dpi n] [-format png] [-pages 1-3] [-quality n] [-password p] -o <dir> <in.pdf>
  formats   what can be read, what can be written, and the paper sizes

The format of a picture is read from its CONTENT, never from its name.
`)
}

func toPDF(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("to-pdf", flag.ContinueOnError)
	fs.SetOutput(errw)
	dpi := fs.Float64("dpi", 72, "pixels of picture per inch of paper")
	page := fs.String("page", "", "paper size; empty means size each page to its own picture")
	margin := fs.Float64("margin", 0, "points of paper left around a fitted picture (needs -page)")
	title := fs.String("title", "", "document title")
	outPath := fs.String("o", "", "the PDF to write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outPath == "" {
		return fmt.Errorf("-o is required: where should the PDF go?")
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("no pictures given")
	}
	size, err := convert.ParsePageSize(*page)
	if err != nil {
		return err
	}
	// ⛔ A margin without a page has nothing to be a margin OF. Silently
	// ignoring it would let somebody believe their pages had one.
	if *margin != 0 && *page == "" {
		return fmt.Errorf("-margin needs -page: a page sized to its own picture has no room to leave")
	}

	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	rs := make([]readerNamed, 0, fs.NArg())
	for _, name := range fs.Args() {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		files = append(files, f)
		rs = append(rs, readerNamed{name: name, f: f})
	}
	pdf, err := readAll(rs, convert.Options{
		DPI: *dpi, Page: size, Margin: *margin, Title: *title,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, pdf, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d page(s), %d bytes\n", *outPath, fs.NArg(), len(pdf))
	return nil
}

type readerNamed struct {
	name string
	f    *os.File
}

// readAll decodes every picture and says WHICH file failed. "reading a
// picture: unknown format" over twenty files is not an error message, it is a
// riddle.
func readAll(rs []readerNamed, opt convert.Options) ([]byte, error) {
	ms := make([]image.Image, 0, len(rs))
	for _, r := range rs {
		m, _, err := convert.Decode(r.f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.name, err)
		}
		ms = append(ms, m)
	}
	return convert.ToPDF(ms, opt)
}

func fromPDF(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("from-pdf", flag.ContinueOnError)
	fs.SetOutput(errw)
	dpi := fs.Float64("dpi", 150, "dots per inch")
	format := fs.String("format", "png", "png, jpeg, gif, bmp or tiff")
	pages := fs.String("pages", "", "which pages, e.g. 1,3,5-9; empty means all")
	quality := fs.Int("quality", 85, "JPEG quality, 1-100")
	password := fs.String("password", "", "password for an encrypted file")
	outPath := fs.String("o", "", "the directory to write into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outPath == "" {
		return fmt.Errorf("-o is required: which directory should the pictures go in?")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("exactly one PDF, got %d", fs.NArg())
	}
	want, err := parseRange(*pages)
	if err != nil {
		return err
	}
	pdf, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	ps, err := convert.FromPDF(pdf, convert.RasterOptions{
		DPI: *dpi, Pages: want, Format: *format, Quality: *quality, Password: *password,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outPath, 0o755); err != nil {
		return err
	}
	base := strings.TrimSuffix(filepath.Base(fs.Arg(0)), filepath.Ext(fs.Arg(0)))
	for _, p := range ps {
		name := filepath.Join(*outPath, fmt.Sprintf("%s-%03d.%s", base, p.Number, p.Format))
		if err := os.WriteFile(name, p.Data, 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "%s: %d page(s) written to %s\n", fs.Arg(0), len(ps), *outPath)
	return nil
}

// parseRange reads "1,3,5-9". An empty string means every page, which is why
// it returns nil rather than an error.
func parseRange(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("page %q is not a number", lo)
		}
		if !isRange {
			out = append(out, a)
			continue
		}
		b, err := strconv.Atoi(strings.TrimSpace(hi))
		if err != nil {
			return nil, fmt.Errorf("page %q is not a number", hi)
		}
		// ⛔ Descending is honoured, not reversed and not refused: "9-5" is a
		// legitimate way to ask for those pages backwards, and silently
		// sorting it would quietly do something else.
		if b < a {
			for i := a; i >= b; i-- {
				out = append(out, i)
			}
			continue
		}
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%q selects no pages", s)
	}
	return out, nil
}

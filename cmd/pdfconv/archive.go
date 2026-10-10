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

	"github.com/go-pdfkit/convert"
)

func fromArchive(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("from-archive", flag.ContinueOnError)
	fs.SetOutput(errw)
	dpi := fs.Float64("dpi", 72, "pixels of picture per inch of paper")
	page := fs.String("page", "", "paper size; empty means size each page to its own picture")
	margin := fs.Float64("margin", 0, "points of paper left around a fitted picture (needs -page)")
	title := fs.String("title", "", "document title")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("from-archive takes an input and an output: " +
			"pdfconv from-archive <in.cbz> <out.pdf>")
	}
	size, err := convert.ParsePageSize(*page)
	if err != nil {
		return err
	}
	if *margin != 0 && *page == "" {
		return fmt.Errorf("-margin needs -page: a page sized to its own picture has no room to leave")
	}

	src, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	pdf, err := convert.ArchiveToPDF(src, convert.Options{
		DPI: *dpi, Page: size, Margin: *margin, Title: *title,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", fs.Arg(0), err)
	}
	if err := os.WriteFile(fs.Arg(1), pdf, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d bytes\n", fs.Arg(1), len(pdf))
	return nil
}

// bundleTo is a seam.
//
// ⛔ Writing to a real temporary file on a working disk cannot fail, so the
// error path below is unreachable from a test — and an unreachable branch is a
// claim nobody has checked. A full disk and a network filesystem both do fail
// here, which is exactly when somebody needs the message.
var bundleTo = convert.Bundle

func bundle(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(errw)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("bundle takes an output and at least one file: " +
			"pdfconv bundle <out.zip> <file> [file …]")
	}
	files := make(map[string][]byte, fs.NArg()-1)
	for _, name := range fs.Args()[1:] {
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files[name] = b
	}
	// ⛔ Written to a temporary beside the target and RENAMED. A bundle of
	// several large PDFs should not be held in memory twice, and a half-written
	// zip left under the final name looks like a complete one — a ZIP's
	// directory is at the end, so it does not even open, but the file is there
	// and dated and somebody will try to send it.
	tmp, err := os.CreateTemp(filepath.Dir(fs.Arg(0)), ".pdfconv-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	// ⛔ The close is folded into the same error, not checked separately. A
	// write that only fails at Close — which is how a full disk and a network
	// filesystem both behave — is the same failure as one that fails earlier,
	// and a branch per step is a branch per step nobody can reach.
	n := &counted{w: tmp}
	err = bundleTo(n, files)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), fs.Arg(0)); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d file(s), %d bytes\n", fs.Arg(0), len(files), n.n)
	return nil
}

// counted is an io.Writer that remembers how much went through it, so the
// size can be reported without asking the filesystem about a file that has
// just been renamed.
type counted struct {
	w io.Writer
	n int64
}

func (c *counted) Write(p []byte) (int, error) {
	k, err := c.w.Write(p)
	c.n += int64(k)
	return k, err
}

func toArchive(args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("to-archive", flag.ContinueOnError)
	fs.SetOutput(errw)
	dpi := fs.Float64("dpi", 150, "dots per inch")
	format := fs.String("format", "png", "png, jpeg, gif, bmp or tiff")
	pages := fs.String("pages", "", "which pages, e.g. 1,3,5-9; empty means all")
	quality := fs.Int("quality", 85, "JPEG quality, 1-100")
	password := fs.String("password", "", "password for an encrypted file")
	title := fs.String("title", "", "the book's name, for the ComicInfo.xml a reader shelves it by")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("to-archive takes an input and an output: " +
			"pdfconv to-archive <in.pdf> <out.cbz>")
	}
	want, err := parseRange(*pages)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}

	// ⛔ Built in memory and written once. A half-written archive left on disk
	// after a failure looks exactly like a complete one — a ZIP's directory is
	// at the END, so a truncated file does not even open, but the file is
	// there and dated and somebody will try.
	var buf bytes.Buffer
	if err := convert.ToArchive(&buf, src, convert.RasterOptions{
		DPI: *dpi, Pages: want, Format: *format, Quality: *quality, Password: *password,
	}, *title); err != nil {
		return fmt.Errorf("%s: %w", fs.Arg(0), err)
	}
	if err := os.WriteFile(fs.Arg(1), buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %d bytes\n", fs.Arg(1), buf.Len())
	return nil
}

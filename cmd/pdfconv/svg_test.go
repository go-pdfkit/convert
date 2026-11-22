// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/convert"
	"github.com/go-pdfkit/reader"
)

// pdfOn writes a PDF of the given pages (content streams) on disk.
func pdfOn(t *testing.T, name string, enc *reader.Encryption, contents ...string) string {
	t.Helper()
	w := reader.NewWriter("1.7")
	if enc != nil {
		w.Encrypt(*enc)
	}
	pagesRef := w.Reserve()
	kids := reader.Array{}
	for _, c := range contents {
		cs := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(c)})
		kids = append(kids, w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": cs}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(len(contents)),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	b, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sq = "0 0 1 rg 10 10 60 40 re f"

func parses(t *testing.T, b []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		if _, err := dec.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("not well-formed: %v", err)
		}
	}
}

func TestToSVGOnePageIsNamedDotSVG(t *testing.T) {
	in := pdfOn(t, "report.pdf", nil, sq)
	out := t.TempDir()
	var o bytes.Buffer
	if err := runTo([]string{"to-svg", "-o", out, in}, &o, io.Discard); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "report.svg"))
	if err != nil {
		t.Fatalf("report.svg is not there: %v", err)
	}
	parses(t, b)
	if !strings.Contains(o.String(), "report.svg: 1 page(s)") {
		t.Errorf("the message is %q", o.String())
	}
}

func TestToSVGSeveralPagesAreNamedUnderscoreSVGDotZip(t *testing.T) {
	in := pdfOn(t, "book.pdf", nil, sq, sq, sq)
	out := filepath.Join(t.TempDir(), "made", "here") // does not exist yet
	if err := run([]string{"to-svg", "-o", out, in}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "book_svg.zip"))
	if err != nil {
		t.Fatalf("book_svg.zip is not there: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "page_1.svg,page_2.svg,page_3.svg" {
		t.Errorf("entries are %v", names)
	}
	// Nothing else was written beside it: no book.svg.
	if _, err := os.Stat(filepath.Join(out, "book.svg")); err == nil {
		t.Error("a single SVG was written beside the zip")
	}
}

func TestToSVGOnePageOfSeveralIsASingleSVG(t *testing.T) {
	// The shape follows the pages SELECTED, as BentoPDF's follows the pages
	// converted.
	in := pdfOn(t, "book.pdf", nil, sq, sq, sq)
	out := t.TempDir()
	if err := run([]string{"to-svg", "-pages", "2", "-o", out, in}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "book.svg")); err != nil {
		t.Errorf("book.svg: %v", err)
	}
}

func TestToSVGRefusesAndWritesNothing(t *testing.T) {
	deep := strings.Repeat("q 10 10 m 90 10 l 50 90 l h W n ", 70) + sq
	good := pdfOn(t, "good.pdf", nil, sq, sq)
	bad := pdfOn(t, "bad.pdf", nil, sq, deep)
	enc := pdfOn(t, "locked.pdf", &reader.Encryption{UserPassword: "pw", OwnerPassword: "ow"}, sq)
	junk := filepath.Join(t.TempDir(), "junk.pdf")
	if err := os.WriteFile(junk, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no input", []string{"to-svg"}, "exactly one PDF"},
		{"two inputs", []string{"to-svg", good, good}, "exactly one PDF"},
		{"unknown flag", []string{"to-svg", "-nonesuch", good}, ""},
		{"bad range", []string{"to-svg", "-pages", "x", good}, "not a number"},
		{"absent", []string{"to-svg", filepath.Join(t.TempDir(), "absent.pdf")}, ""},
		{"junk", []string{"to-svg", junk}, "junk.pdf"},
		{"page too deep", []string{"to-svg", bad}, "page 2:"},
		{"locked", []string{"to-svg", enc}, "opening the PDF"},
		{"wrong password", []string{"to-svg", "-password", "no", enc}, "opening the PDF"},
		{"page past the end", []string{"to-svg", "-pages", "9", good}, "this file has 2"},
	} {
		out := t.TempDir()
		var err error
		func() {
			// A panic is a refusal that was not made: reported as the assertion
			// it is.
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panicked instead of refusing: %v", c.name, r)
					err = errors.New(c.want)
				}
			}()
			err = run(append([]string{c.args[0], "-o", out}, c.args[1:]...))
		}()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
		if es, _ := os.ReadDir(out); len(es) != 0 {
			t.Errorf("%s: %d file(s) left behind", c.name, len(es))
		}
	}
	// The failing page of the deep file is reported by the process, not a crash.
	var e bytes.Buffer
	var code int
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panicked instead of exiting 1: %v", r)
			}
		}()
		code = mainish([]string{"to-svg", "-o", t.TempDir(), bad}, io.Discard, &e)
	}()
	if code != 1 || !strings.Contains(e.String(), "pdfconv:") || !strings.Contains(e.String(), "page 2:") {
		t.Errorf("exit %d, stderr %q", code, e.String())
	}
}

func TestToSVGOpensAnEncryptedFileWithItsPassword(t *testing.T) {
	enc := pdfOn(t, "locked.pdf", &reader.Encryption{UserPassword: "pw", OwnerPassword: "ow"}, sq)
	out := t.TempDir()
	if err := run([]string{"to-svg", "-password", "pw", "-o", out, enc}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "locked.svg")); err != nil {
		t.Error(err)
	}
}

func TestToSVGCannotWriteWhereItIsPointed(t *testing.T) {
	in := pdfOn(t, "a.pdf", nil, sq)
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory that is a file: refused by the step that makes the directory,
	// not by the write that would have failed anyway one line later.
	if err := run([]string{"to-svg", "-o", filepath.Join(file, "sub"), in}); err == nil ||
		!strings.Contains(err.Error(), "mkdir") {
		t.Errorf("a directory under a file: %v", err)
	}
	// An output name that is a directory.
	out := t.TempDir()
	if err := os.Mkdir(filepath.Join(out, "a.svg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"to-svg", "-o", out, in}); err == nil || !strings.Contains(err.Error(), "a.svg") {
		t.Errorf("a directory overwritten by a file: %v", err)
	}
	// An input that is not there is the file system's refusal, not the
	// converter's.
	err := run([]string{"to-svg", "-o", out, filepath.Join(t.TempDir(), "absent.pdf")})
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an absent input: %v", err)
	}
}

func TestToSVGTheRunsTotalCeilingStopsOnThePageThatCrossesIt(t *testing.T) {
	in := pdfOn(t, "book.pdf", nil, sq, sq)
	out := t.TempDir()
	defer func(v int) { convert.MaxSVGBytes = v }(convert.MaxSVGBytes)
	convert.MaxSVGBytes = 250 // one page is 202 bytes
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panicked instead of refusing: %v", r)
			}
		}()
		err = run([]string{"to-svg", "-o", out, in})
	}()
	if err == nil || !strings.Contains(err.Error(), "page 2:") {
		t.Errorf("got %v", err)
	}
	if es, _ := os.ReadDir(out); len(es) != 0 {
		t.Errorf("%d file(s) left behind", len(es))
	}
}

func TestToSVGAnArchiveThatCannotBeBuiltIsReportedAndLeavesNothing(t *testing.T) {
	in := pdfOn(t, "book.pdf", nil, sq, sq)
	out := t.TempDir()
	defer func(f func(io.Writer, []convert.SVGPage) error) { svgArchive = f }(svgArchive)
	svgArchive = func(io.Writer, []convert.SVGPage) error { return errors.New("no room") }
	err := run([]string{"to-svg", "-o", out, in})
	if err == nil || !strings.Contains(err.Error(), "book.pdf: no room") {
		t.Errorf("got %v", err)
	}
	if es, _ := os.ReadDir(out); len(es) != 0 {
		t.Errorf("%d file(s) left behind", len(es))
	}
}

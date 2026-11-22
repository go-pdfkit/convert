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
		{"wrong password", []string{"to-svg", "-password-file", pwFile(t, "no"), enc}, "opening the PDF"},
		{"password file absent", []string{"to-svg", "-password-file", filepath.Join(t.TempDir(), "none"), enc}, "password file"},
		{"the old flag is gone", []string{"to-svg", "-password", "pw", enc}, "not defined"},
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
	if err := run([]string{"to-svg", "-password-file", pwFile(t, "pw\n"), "-o", out, enc}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "locked.svg")); err != nil {
		t.Error(err)
	}
}

// pwFile writes a password file holding exactly content.
func pwFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestThePasswordComesFromAFileOrStandardInputWithOneNewlineStripped(t *testing.T) {
	defer func(r io.Reader) { stdin = r }(stdin)
	for _, c := range []struct{ in, want string }{
		{"pw", "pw"}, {"pw\n", "pw"}, {"pw\r\n", "pw"}, {"pw\n\n", "pw\n"}, {"p w\r", "p w\r"}, {"", ""},
	} {
		if got, err := readPassword(pwFile(t, c.in)); err != nil || got != c.want {
			t.Errorf("file %q: %q, %v", c.in, got, err)
		}
		stdin = strings.NewReader(c.in)
		if got, err := readPassword("-"); err != nil || got != c.want {
			t.Errorf("stdin %q: %q, %v", c.in, got, err)
		}
	}
	if got, err := readPassword(""); err != nil || got != "" {
		t.Errorf("no file: %q, %v", got, err)
	}
	if got, err := readPassword(pwFile(t, strings.Repeat("x", maxPasswordFile))); err != nil || len(got) != maxPasswordFile {
		t.Errorf("a file of exactly the limit: %d bytes, %v", len(got), err)
	}
	if _, err := readPassword(filepath.Join(t.TempDir(), "none")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an absent file: %v", err)
	}
	if _, err := readPassword(pwFile(t, strings.Repeat("x", maxPasswordFile+1))); err == nil ||
		!strings.Contains(err.Error(), "longer than") {
		t.Errorf("an oversized file: %v", err)
	}
	if _, err := readPassword(t.TempDir()); err == nil || !strings.Contains(err.Error(), "password file") {
		t.Errorf("a directory: %v", err)
	}
	stdin = failingReader{}
	if _, err := readPassword("-"); err == nil || !strings.Contains(err.Error(), "password file -") {
		t.Errorf("a pipe that fails: %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestEveryVerbThatTakesAPasswordTakesItFromAFileAndNotFromArgv(t *testing.T) {
	enc := pdfOn(t, "locked.pdf", &reader.Encryption{UserPassword: "pw", OwnerPassword: "ow"}, sq)
	pw := pwFile(t, "pw\n")
	for _, args := range [][]string{
		{"from-pdf", "-password-file", pw, "-o", t.TempDir(), enc},
		{"to-archive", "-password-file", pw, enc, filepath.Join(t.TempDir(), "a.cbz")},
		{"redraw", "-password-file", pw, enc, filepath.Join(t.TempDir(), "r.pdf")},
	} {
		if err := run(args); err != nil {
			t.Errorf("%s with a password file: %v", args[0], err)
		}
		bad := append([]string{args[0], "-password", "pw"}, args[3:]...)
		if err := run(bad); err == nil || !strings.Contains(err.Error(), "not defined") {
			t.Errorf("%s still takes -password: %v", args[0], err)
		}
		missing := append([]string{args[0], "-password-file", filepath.Join(t.TempDir(), "none")}, args[3:]...)
		if err := run(missing); err == nil || !strings.Contains(err.Error(), "password file") {
			t.Errorf("%s with an absent password file: %v", args[0], err)
		}
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

// full is a writer whose disk filled.
type full struct{}

func (full) Write([]byte) (int, error) { return 0, errors.New("no room") }

func TestToSVGAWriteThatFailsIsReportedAndLeavesNothing(t *testing.T) {
	for _, name := range []string{"one", "several"} {
		in := pdfOn(t, "book.pdf", nil, sq, sq)
		args := []string{"to-svg", "-pages", "1", "-o"}
		if name == "several" {
			args = []string{"to-svg", "-o"}
		}
		out := t.TempDir()
		defer func(f func(io.Writer) io.Writer) { wrapOut = f }(wrapOut)
		wrapOut = func(io.Writer) io.Writer { return full{} }
		err := run(append(args, out, in))
		if err == nil || !strings.Contains(err.Error(), "no room") {
			t.Errorf("%s: got %v", name, err)
		}
		if es, _ := os.ReadDir(out); len(es) != 0 {
			t.Errorf("%s: %d file(s) left behind", name, len(es))
		}
	}
}

func TestToSVGNeverWritesThroughALinkOrOverAFile(t *testing.T) {
	in := pdfOn(t, "in.pdf", nil, sq)
	out := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(out, "in.svg")

	// A symlink at the target, and a dangling one: neither is followed.
	for _, dest := range []string{victim, filepath.Join(out, "nowhere")} {
		if err := os.Symlink(dest, target); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"to-svg", "-o", out, in}); err == nil || !strings.Contains(err.Error(), "not overwritten") {
			t.Errorf("a symlink to %s: %v", dest, err)
		}
		os.Remove(target)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious\n" {
		t.Errorf("the file behind the link now holds %q", b)
	}
	if _, err := os.Lstat(filepath.Join(out, "nowhere")); err == nil {
		t.Error("a dangling link was followed and its target created")
	}

	// An existing file is refused, and kept.
	if err := os.WriteFile(target, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"to-svg", "-o", out, in}); err == nil || !strings.Contains(err.Error(), "not overwritten") {
		t.Errorf("an existing file: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "mine\n" {
		t.Errorf("the existing file now holds %q", b)
	}
	// Only the file that was there: no temporary file survives a refusal.
	if es, _ := os.ReadDir(out); len(es) != 1 {
		t.Errorf("%d entries in the directory", len(es))
	}
}

func TestToSVGWhereHardLinksAreRefusedItCreatesExclusively(t *testing.T) {
	defer func(f func(string, string) error) { linkFile = f }(linkFile)
	linkFile = func(string, string) error { return errors.New("operation not supported") }
	in := pdfOn(t, "in.pdf", nil, sq)
	out := t.TempDir()
	if err := run([]string{"to-svg", "-o", out, in}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "in.svg"))
	if err != nil {
		t.Fatal(err)
	}
	parses(t, b)
	// The fallback refuses an existing name too, a symlink included.
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(out, "in.svg"))
	if err := os.Symlink(victim, filepath.Join(out, "in.svg")); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"to-svg", "-o", out, in}); err == nil || !strings.Contains(err.Error(), "not overwritten") {
		t.Errorf("a symlink at the target: %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious\n" {
		t.Errorf("the file behind the link now holds %q", b)
	}
	// A directory of that name is refused by the create, not as "exists" of a
	// file.
	os.Remove(filepath.Join(out, "in.svg"))
	if err := os.Mkdir(filepath.Join(out, "in.svg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"to-svg", "-o", out, in}); err == nil {
		t.Error("a directory was overwritten")
	}
	// A name that cannot be created for another reason is that reason.
	if err := publish(filepath.Join(out, "absent.tmp"), filepath.Join(out, "x")); err == nil ||
		!strings.Contains(err.Error(), "absent.tmp") {
		t.Error("a missing temporary file was published")
	}
	if err := publish(filepath.Join(out, "in.svg", ".."), filepath.Join(out, "no", "such", "x")); err == nil {
		t.Error("an uncreatable name was published")
	}
}

func TestToSVGAFallbackCopyThatFailsRemovesWhatItCreated(t *testing.T) {
	defer func(f func(string, string) error) { linkFile = f }(linkFile)
	linkFile = func(string, string) error { return errors.New("operation not supported") }
	dir := t.TempDir()
	// A directory as the source: opening works, reading fails.
	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := publish(src, target); err == nil {
		t.Error("a failed copy was reported as published")
	}
	if _, err := os.Lstat(target); err == nil {
		t.Error("the half-made target was left behind")
	}
}

func TestToSVGADirectoryThatCannotBeWrittenInIsRefusedAndLeavesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	in := pdfOn(t, "in.pdf", nil, sq)
	out := t.TempDir()
	if err := os.Chmod(out, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(out, 0o700)
	if err := run([]string{"to-svg", "-o", out, in}); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Errorf("got %v", err)
	}
}

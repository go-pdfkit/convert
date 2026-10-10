// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func entryNames(t *testing.T, b []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func TestBundleLeavesItsInputAlone(t *testing.T) {
	// ⛔ The one tool here whose job is to change nothing. Several PDFs that
	// have to travel together are a packaging problem, and running them
	// through a renderer to zip them would lose every byte of what made them
	// worth sending.
	files := map[string][]byte{
		"a.pdf": []byte("%PDF-1.7 first"),
		"b.pdf": []byte("%PDF-1.7 second"),
	}
	var out bytes.Buffer
	if err := Bundle(&out, files); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("%d entries", len(zr.File))
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		io.Copy(&sb, rc)
		rc.Close()
		if string(files[f.Name]) != sb.String() {
			t.Errorf("%s came out as %q", f.Name, sb.String())
		}
	}
}

func TestADirectoryIsStrippedFromAnEntryName(t *testing.T) {
	// ⛔ An archive whose entries are absolute paths tells a stranger where the
	// files lived — whose home directory, which client, what the project is
	// called. A bundle is a thing people send.
	var out bytes.Buffer
	if err := Bundle(&out, map[string][]byte{
		"/Users/someone/clients/acme/report.pdf": []byte("x"),
	}); err != nil {
		t.Fatal(err)
	}
	got := entryNames(t, out.Bytes())
	if len(got) != 1 || got[0] != "report.pdf" {
		t.Errorf("the entry is %v", got)
	}
	if strings.Contains(out.String(), "clients/acme") {
		t.Error("the path survived into the archive")
	}
}

func TestTwoFilesOfTheSameNameBothSurvive(t *testing.T) {
	// ⛔ Two report.pdf from different directories would otherwise become one
	// entry, and the second would silently replace the first in every reader
	// that takes the last match. Losing a file in a tool whose only job is not
	// to lose files is the worst thing it could do.
	var out bytes.Buffer
	if err := Bundle(&out, map[string][]byte{
		"jan/report.pdf": []byte("january"),
		"feb/report.pdf": []byte("february"),
		"mar/report.pdf": []byte("march"),
	}); err != nil {
		t.Fatal(err)
	}
	got := entryNames(t, out.Bytes())
	if len(got) != 3 {
		t.Fatalf("%d entries: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		if seen[n] {
			t.Errorf("%q appears twice: %v", n, got)
		}
		seen[n] = true
	}
	if !seen["report.pdf"] {
		t.Errorf("the first one lost its name: %v", got)
	}
}

func TestTwoBundlesOfTheSameFilesAreTheSameArchive(t *testing.T) {
	// ⛔ Go randomises map iteration on purpose, so without an ordering two
	// bundles of the same files differ and no checksum of one means anything.
	files := map[string][]byte{
		"z.pdf": []byte("z"), "a.pdf": []byte("a"), "m10.pdf": []byte("m10"),
		"m2.pdf": []byte("m2"), "b.pdf": []byte("b"),
	}
	var first string
	for i := 0; i < 20; i++ {
		var out bytes.Buffer
		if err := Bundle(&out, files); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(entryNames(t, out.Bytes()), ",")
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d gave %s, run 0 gave %s", i, got, first)
		}
	}
	// And the order is natural, like everywhere else here.
	if !strings.Contains(first, "m2.pdf,m10.pdf") {
		t.Errorf("the order is %s, and m10 comes after m2", first)
	}
}

func TestBundlingNothingIsRefused(t *testing.T) {
	if err := Bundle(&bytes.Buffer{}, nil); err == nil {
		t.Error("an empty bundle was written")
	}
}

func TestBundleReportsAWriterThatFails(t *testing.T) {
	files := map[string][]byte{"a.pdf": bytes.Repeat([]byte("x"), 9000)}
	if err := Bundle(&shortWriter{limit: 10}, files); err == nil {
		t.Error("a writer that fills up was reported as success")
	}
	was := newArchive
	t.Cleanup(func() { newArchive = was })
	newArchive = func(io.Writer) archiveWriter { return &refusingArchive{onCreate: true, n: 1} }
	err := Bundle(&bytes.Buffer{}, files)
	if err == nil {
		t.Fatal("a refusing archive was reported as success")
	}
	if !strings.Contains(err.Error(), "a.pdf") {
		t.Errorf("the refusal is %q and does not name the file", err)
	}
}

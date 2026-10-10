// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"path"
	"sort"
	"strings"
	"unicode"
)

// An archive of pictures is what a comic book is: a ZIP whose entries are the
// pages, in the order their NAMES put them. CBZ, CBR's zipped cousin, and
// "here are the scans in a zip" are all the same thing.
// ⛔ Variables rather than constants, and that is not a style choice: a
// ceiling of half a gigabyte cannot be reached by a test that has to run in a
// second, so a constant here would be a guard nobody could exercise — and this
// package has already shipped two of those. A test lowers them; nothing else
// should.
var (
	// MaxArchiveEntries is how many files an archive may hold.
	//
	// It bounds an allocation made before anything is read, the same way
	// go-odf/odf's does: a ZIP's central directory costs about forty-six bytes
	// an entry, so a small file can declare a great many.
	MaxArchiveEntries = 4096

	// MaxArchiveEntryBytes is how large any one entry may be once opened, and
	// MaxArchiveBytes how much the whole archive may come to. One entry under
	// the ceiling says nothing about a thousand of them.
	MaxArchiveEntryBytes uint64 = 128 << 20
	MaxArchiveBytes      uint64 = 512 << 20
)

// ArchiveToPDF lays the pictures in a ZIP onto pages, in the order their names
// put them.
//
// ⛔ The ORDER is the whole job. A comic archive names its pages and nothing
// else records the sequence, so "page10.jpg" has to follow "page2.jpg" — which
// a byte-wise sort does not do, and which is the single defect every naive
// reader of this format has. See naturalLess.
//
// Entries that are not pictures are skipped rather than refused: real archives
// carry a ComicInfo.xml, a thumbnail, a __MACOSX folder, a readme.
func ArchiveToPDF(src []byte, opt Options) ([]byte, error) {
	names, data, err := readArchive(src)
	if err != nil {
		return nil, err
	}

	imgs := make([]image.Image, 0, len(names))
	var skipped int
	for _, n := range names {
		m, _, err := decodeWithin(bytes.NewReader(data[n]), opt.MaxPixels)
		if err != nil {
			skipped++
			continue
		}
		imgs = append(imgs, m)
	}
	if len(imgs) == 0 {
		// ⛔ Said out loud, with the count. "No pictures" and "there were
		// forty files and none of them was a picture" are different things,
		// and the second is usually a wrong file rather than an empty one.
		return nil, fmt.Errorf("no pictures in the archive: %d entr%s held nothing readable",
			len(names), plural(len(names)))
	}
	return ToPDF(imgs, opt)
}

// readArchive opens a ZIP and returns its entry names in reading order, with
// the bytes of each.
func readArchive(src []byte) ([]string, map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return nil, nil, fmt.Errorf("opening the archive: %w", err)
	}
	if len(zr.File) > MaxArchiveEntries {
		return nil, nil, fmt.Errorf("the archive holds %d entries, past the %d allowed",
			len(zr.File), MaxArchiveEntries)
	}

	var names []string
	data := make(map[string][]byte, len(zr.File))
	var total uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || hidden(f.Name) {
			continue
		}
		// The declared size decides, before a byte is inflated — a ZIP bomb
		// costs nothing to refuse, because its own header says what it costs.
		left := MaxArchiveEntryBytes
		if room := MaxArchiveBytes - total; room < left {
			left = room
		}
		if f.UncompressedSize64 > left {
			return nil, nil, fmt.Errorf("%s says it opens to %d bytes, past the %d allowed",
				f.Name, f.UncompressedSize64, left)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, int64(left)+1))
		rc.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		total += uint64(len(b))
		names = append(names, f.Name)
		data[f.Name] = b
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("the archive holds no files")
	}
	sort.Slice(names, func(a, b int) bool { return naturalLess(names[a], names[b]) })
	return names, data, nil
}

// hidden says whether an entry is one an archiver added rather than one a
// person put there.
func hidden(name string) bool {
	if strings.HasPrefix(name, "__MACOSX/") || strings.Contains(name, "/__MACOSX/") {
		return true
	}
	base := path.Base(name)
	return strings.HasPrefix(base, ".")
}

// naturalLess orders names the way a person reads them: a run of digits
// compares as a NUMBER.
//
// ⛔ This is the defect every naive reader of this format has. Sorted as
// bytes, "page10.jpg" comes before "page2.jpg", so a hundred-page comic reads
// 1, 10, 11, … 2, 20, … and nothing anywhere says so: every page is present,
// the count is right, and the file opens.
func naturalLess(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		ca, cb := ra[i], rb[j]
		if unicode.IsDigit(ca) && unicode.IsDigit(cb) {
			// Both at a run of digits: compare the runs as numbers, by
			// length-after-leading-zeros then lexically. Done without parsing,
			// so a page numbered past what an int holds still sorts.
			si, sj := i, j
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		// ⛔ Case-insensitive, because an archive mixes "Page01" and "page02"
		// and a reader who sees them in two groups has lost the order as
		// surely as if the numbers were wrong.
		la, lb := unicode.ToLower(ca), unicode.ToLower(cb)
		if la != lb {
			return la < lb
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}

// Bundle packs whole files into a ZIP, unchanged.
//
// ⛔ It converts NOTHING, and that is the point: it is the one tool here whose
// job is to leave its input alone. Several PDFs that have to travel together —
// a submission, a set of invoices, a chapter each — are a packaging problem,
// not a conversion one, and running them through a renderer to put them in a
// zip would lose every byte of what made them worth sending.
//
// The names are the ones given, with their directories stripped: an archive
// whose entries are absolute paths from somebody's disk tells a stranger where
// the files lived.
func Bundle(w io.Writer, files map[string][]byte) error {
	if len(files) == 0 {
		return fmt.Errorf("no files: an empty archive is not a bundle")
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	// Sorted, because Go randomises map iteration and two bundles of the same
	// files must be the same archive or no checksum of one means anything.
	sort.Slice(names, func(a, b int) bool { return naturalLess(names[a], names[b]) })

	zw := newArchive(w)
	seen := map[string]int{}
	for _, n := range names {
		base := path.Base(n)
		// ⛔ Two files called report.pdf from different directories would
		// otherwise become one entry, and the second would silently replace
		// the first in every reader that takes the last match.
		if k := seen[base]; k > 0 {
			ext := path.Ext(base)
			base = strings.TrimSuffix(base, ext) + fmt.Sprintf("-%d", k+1) + ext
		}
		seen[path.Base(n)]++
		f, err := zw.Create(base)
		if err == nil {
			_, err = f.Write(files[n])
		}
		if err != nil {
			return fmt.Errorf("packing %s: %w", base, err)
		}
	}
	return zw.Close()
}

// archiveWriter is the part of *zip.Writer this package uses.
type archiveWriter interface {
	Create(name string) (io.Writer, error)
	Close() error
}

// newArchive is a seam.
//
// ⛔ archive/zip wraps its output in a buffer and defers every error to Close,
// so the per-entry error path below cannot be reached through an ordinary
// io.Writer however full the disk is — which makes it a claim nobody has
// tested rather than a guard. Rather than delete a check on an API that does
// return errors, or leave a line nothing can reach, the constructor is a
// variable and a test hands it one that fails.
var newArchive = func(w io.Writer) archiveWriter { return zip.NewWriter(w) }

// ToArchive draws the pages and writes them into a ZIP: one picture per page,
// named so that they come back in order, with a ComicInfo.xml beside them.
//
// ⛔ The ComicInfo.xml is not decoration. It is what Komga, Kavita and Calibre
// read to know how many pages a book has and what it is called; without it a
// reader shows the archive as an untitled pile and has to open every entry to
// count it. A CBZ with no metadata is a CBZ a library will not shelve.
// The title, when given, goes into that metadata; it is the book's name, not
// a filename.
func ToArchive(w io.Writer, pdf []byte, opt RasterOptions, title string) error {
	ps, err := FromPDF(pdf, opt)
	if err != nil {
		return err
	}
	zw := newArchive(w)
	if err := writeComicInfo(zw, title, ps); err != nil {
		return err
	}
	for _, p := range ps {
		// ⛔ Zero-padded to the width of the LAST page number, not to a fixed
		// four. Padded too narrowly, a thousand-page document reads 1, 10,
		// 100, 1000, 101 in every reader that sorts by name — which is every
		// reader of this format.
		name := fmt.Sprintf("%0*d.%s", width(len(ps)), p.Number, p.Format)
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

// writeComicInfo writes the metadata entry comic readers look for.
//
// ⛔ It goes in FIRST, before the pages. The file is conventionally the first
// entry of the archive, and more to the point a reader that streams the ZIP
// finds it without reading to the end — which, for a book of three hundred
// scans, is the difference between a library that shelves it instantly and one
// that stalls.
//
// ⛔ The title is ESCAPED. It comes from a caller, by way of a filename, and a
// title holding an ampersand would otherwise produce an XML file that no
// reader can parse — which is a worse failure than no metadata at all, because
// it looks like metadata.
func writeComicInfo(zw archiveWriter, title string, ps []Page) error {
	f, err := zw.Create("ComicInfo.xml")
	if err != nil {
		return fmt.Errorf("writing ComicInfo.xml: %w", err)
	}
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<ComicInfo xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` + "\n")
	if title != "" {
		// ⛔ The error is ignored on purpose, not forgotten: xml.EscapeText
		// can only fail through its WRITER, and a strings.Builder never does.
		// A check here would be a branch nothing can reach — this package has
		// already removed two of those.
		var esc strings.Builder
		_ = xml.EscapeText(&esc, []byte(title))
		fmt.Fprintf(&b, "  <Title>%s</Title>\n", esc.String())
	}
	fmt.Fprintf(&b, "  <PageCount>%d</PageCount>\n", len(ps))
	b.WriteString("  <Pages>\n")
	for i, p := range ps {
		// Image is the INDEX in the archive, counted from zero — not the page
		// number in the document it came from. A reader uses it to address the
		// entry, and a book made from pages 4 to 9 of something still starts
		// at zero.
		fmt.Fprintf(&b, "    <Page Image=\"%d\" ImageSize=\"%d\"/>\n", i, len(p.Data))
	}
	b.WriteString("  </Pages>\n</ComicInfo>\n")
	if _, err := io.WriteString(f, b.String()); err != nil {
		return fmt.Errorf("writing ComicInfo.xml: %w", err)
	}
	return nil
}

// width is how many digits the largest page number needs.
func width(n int) int {
	w := 1
	for n >= 10 {
		n /= 10
		w++
	}
	return w
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

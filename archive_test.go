// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"
)

// barPNG is a picture with n black columns at its left. The number is readable
// off the PIXELS, which is the only way to tell what order the pages came back
// in without asking the sorter whether it sorted.
func barPNG(t *testing.T, n int) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
			if x < n {
				c = color.NRGBA{A: 255}
			}
			m.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// cbz builds an archive from name -> contents, in the order given.
func cbz(t *testing.T, entries ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bars draws a PDF and reports each page's black-bar width.
func bars(t *testing.T, pdf []byte) []int {
	t.Helper()
	ps, err := FromPDF(pdf, RasterOptions{DPI: 72, Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int, 0, len(ps))
	for _, p := range ps {
		m, err := png.Decode(bytes.NewReader(p.Data))
		if err != nil {
			t.Fatal(err)
		}
		b := m.Bounds()
		y := b.Min.Y + b.Dy()/2
		dark := 0
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := m.At(x, y).RGBA()
			if r>>8 < 100 && g>>8 < 100 && bb>>8 < 100 {
				dark++
			}
		}
		out = append(out, dark)
	}
	return out
}

func TestPageTenComesAfterPageTwo(t *testing.T) {
	// ⛔ The defect every naive reader of this format has. Sorted as bytes,
	// "page10.png" comes before "page2.png", so a hundred-page comic reads
	// 1, 10, 11, … 2, 20, … — and nothing says so: every page is present, the
	// count is right, and the file opens.
	//
	// The order is read off the PIXELS, not off the names, because asking the
	// sorter what order it produced is not a test of the sorter.
	src := cbz(t,
		[2]string{"page10.png", string(barPNG(t, 10))},
		[2]string{"page2.png", string(barPNG(t, 2))},
		[2]string{"page1.png", string(barPNG(t, 1))},
		[2]string{"page20.png", string(barPNG(t, 20))},
		[2]string{"page3.png", string(barPNG(t, 3))},
	)
	pdf, err := ArchiveToPDF(src, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	got := bars(t, pdf)
	want := []int{1, 2, 3, 10, 20}
	if len(got) != len(want) {
		t.Fatalf("%d pages, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the pages came back %v, want %v — a byte-wise sort gives "+
				"1, 10, 2, 20, 3", got, want)
		}
	}
}

func TestCaseDoesNotSplitTheOrderInTwo(t *testing.T) {
	// ⛔ An archive mixes "Page01" and "page02" all the time, and a
	// case-sensitive sort puts every capital before every lower-case letter —
	// so the book reads in two halves. That is a reordering nobody notices
	// until they read it.
	src := cbz(t,
		[2]string{"Page3.png", string(barPNG(t, 3))},
		[2]string{"page1.png", string(barPNG(t, 1))},
		[2]string{"PAGE2.png", string(barPNG(t, 2))},
	)
	pdf, err := ArchiveToPDF(src, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	if got := bars(t, pdf); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("mixed case gave %v", got)
	}
}

func TestLeadingZeroesDoNotChangeTheOrder(t *testing.T) {
	src := cbz(t,
		[2]string{"p007.png", string(barPNG(t, 7))},
		[2]string{"p0002.png", string(barPNG(t, 2))},
		[2]string{"p10.png", string(barPNG(t, 10))},
	)
	pdf, err := ArchiveToPDF(src, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	if got := bars(t, pdf); len(got) != 3 || got[0] != 2 || got[1] != 7 || got[2] != 10 {
		t.Errorf("padded numbers gave %v", got)
	}
}

func TestANumberTooBigForAnIntStillSorts(t *testing.T) {
	// ⛔ The comparison does not PARSE the digits, so a run longer than an int
	// can hold still orders correctly. Parsing would overflow and the two
	// would compare equal, which puts them in whatever order the archive
	// happened to list them.
	if !naturalLess("p99999999999999999999.png", "p999999999999999999999.png") {
		t.Error("a twenty-digit number did not sort before a twenty-one-digit one")
	}
	if naturalLess("p999999999999999999999.png", "p99999999999999999999.png") {
		t.Error("the comparison is not antisymmetric on very long numbers")
	}
}

func TestWhatAnArchiverAddedIsNotAPage(t *testing.T) {
	// A real comic archive carries a ComicInfo.xml, a cover thumbnail, a
	// __MACOSX folder and a readme. Refusing the whole file over them would
	// refuse most real archives; counting them as pages puts rubbish in the
	// middle of the book.
	// ⛔ The intruders hold REAL pictures. A first version filled them with
	// text, so they failed to decode and were skipped whatever the filter did
	// — the mutation removing the filter survived, because the fixture gave it
	// nothing to do. An archiver's copy of a page is a decodable page.
	src := cbz(t,
		[2]string{"__MACOSX/._page1.png", string(barPNG(t, 30))},
		[2]string{".thumbnail.png", string(barPNG(t, 31))},
		[2]string{"ComicInfo.xml", "<xml/>"},
		[2]string{"page1.png", string(barPNG(t, 1))},
		[2]string{"page2.png", string(barPNG(t, 2))},
	)
	pdf, err := ArchiveToPDF(src, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	got := bars(t, pdf)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("the book came back as %v, and only pages 1 and 2 are pages", got)
	}
}

func TestAnArchiveOfNoPicturesSaysHowManyItLookedAt(t *testing.T) {
	// ⛔ "No pictures" and "there were forty files and none was a picture" are
	// different things, and the second is usually a wrong file rather than an
	// empty one.
	src := cbz(t,
		[2]string{"notes.txt", "hello"},
		[2]string{"info.xml", "<x/>"},
	)
	_, err := ArchiveToPDF(src, Options{})
	if err == nil {
		t.Fatal("an archive of no pictures was converted")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("the refusal is %q and does not say how many were looked at", err)
	}
}

func TestAnEmptyOrBrokenArchiveIsRefused(t *testing.T) {
	if _, err := ArchiveToPDF([]byte("not a zip"), Options{}); err == nil {
		t.Error("something that is not a ZIP was opened")
	}
	if _, err := ArchiveToPDF(cbz(t), Options{}); err == nil {
		t.Error("an empty archive was converted")
	}
	if _, err := ArchiveToPDF(cbz(t, [2]string{"d/", ""}), Options{}); err == nil {
		t.Error("an archive of one directory was converted")
	}
}

func TestAnArchiveCannotAskForMoreThanTheCeilings(t *testing.T) {
	// ⛔ The same ceilings as every other reader of a ZIP in this fleet: the
	// declared size decides, before a byte is inflated.
	var many [][2]string
	for i := 0; i <= MaxArchiveEntries; i++ {
		many = append(many, [2]string{"f" + itoa(i), "x"})
	}
	if _, err := ArchiveToPDF(cbz(t, many...), Options{}); err == nil {
		t.Error("an archive of more entries than allowed was read")
	} else if !strings.Contains(err.Error(), "entries") {
		t.Errorf("refused as %q", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestAnArchiveComesBackOutInOrder(t *testing.T) {
	// The round trip, which is the thing a reader actually does: an archive in,
	// a PDF, an archive out, and the same book.
	src := cbz(t,
		[2]string{"page9.png", string(barPNG(t, 9))},
		[2]string{"page10.png", string(barPNG(t, 10))},
		[2]string{"page1.png", string(barPNG(t, 1))},
	)
	pdf, err := ArchiveToPDF(src, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ToArchive(&out, pdf, RasterOptions{DPI: 72, Format: "png"}, ""); err != nil {
		t.Fatal(err)
	}
	again, err := ArchiveToPDF(out.Bytes(), Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	if got := bars(t, again); len(got) != 3 || got[0] != 1 || got[1] != 9 || got[2] != 10 {
		t.Errorf("the round trip gave %v", got)
	}
}

func TestPageNamesArePaddedToTheLastPageNumber(t *testing.T) {
	// ⛔ Padded too narrowly, a thousand-page document reads 1, 10, 100, 1000,
	// 101 in every reader that sorts by name — which is every reader of this
	// format. The width comes from the COUNT, not from a fixed four.
	m := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	pages := make([]image.Image, 12)
	for i := range pages {
		pages[i] = m
	}
	pdf, err := ToPDF(pages, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ToArchive(&out, pdf, RasterOptions{DPI: 36, Format: "png"}, ""); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	// Twelve pages and the metadata entry, which comes first.
	if len(zr.File) != 13 {
		t.Fatalf("%d entries", len(zr.File))
	}
	if zr.File[0].Name != "ComicInfo.xml" {
		t.Errorf("the first entry is %q, and a reader that streams the archive "+
			"should meet the metadata before three hundred scans", zr.File[0].Name)
	}
	if zr.File[1].Name != "01.png" {
		t.Errorf("the first page is %q, and twelve pages need two digits", zr.File[1].Name)
	}
	if zr.File[12].Name != "12.png" {
		t.Errorf("the last page is %q", zr.File[12].Name)
	}
}

func TestAMetadataEntryThatCannotBeWrittenIsReported(t *testing.T) {
	// ⛔ It is the FIRST entry, so a disk that is already full fails here
	// rather than on a page — and a book that came back with pages and no
	// metadata would be shelved as an untitled pile without anything saying
	// why.
	was := newArchive
	t.Cleanup(func() { newArchive = was })
	m := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	pdf, err := ToPDF([]image.Image{m}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	for _, onCreate := range []bool{true, false} {
		newArchive = func(io.Writer) archiveWriter {
			return &refusingArchive{onCreate: onCreate, n: 1} // n=1: fail on the FIRST
		}
		err := ToArchive(&bytes.Buffer{}, pdf, RasterOptions{DPI: 36}, "x")
		if err == nil {
			t.Fatalf("onCreate=%v: a refusing archive was reported as success", onCreate)
		}
		if !strings.Contains(err.Error(), "ComicInfo.xml") {
			t.Errorf("onCreate=%v: the refusal is %q", onCreate, err)
		}
	}
}

func TestTheMetadataSaysWhatAReaderNeeds(t *testing.T) {
	// ⛔ Komga, Kavita and Calibre read ComicInfo.xml to know what a book is
	// called and how many pages it has. Without it they show an untitled pile
	// and have to open every entry to count it.
	m := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	pdf, err := ToPDF([]image.Image{m, m, m}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// ⛔ A title with an ampersand in it. It comes from a caller by way of a
	// filename, and unescaped it produces an XML file no reader can parse —
	// which is worse than no metadata, because it LOOKS like metadata.
	if err := ToArchive(&out, pdf, RasterOptions{DPI: 36, Format: "png"},
		`Tom & Jerry <"best of">`); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var sb strings.Builder
	if _, err := io.Copy(&sb, rc); err != nil {
		t.Fatal(err)
	}
	info := sb.String()

	// It parses. That is the only test of escaping that cannot be fooled.
	var parsed struct {
		Title     string `xml:"Title"`
		PageCount int    `xml:"PageCount"`
		Pages     []struct {
			Image int `xml:"Image,attr"`
		} `xml:"Pages>Page"`
	}
	if err := xml.Unmarshal([]byte(info), &parsed); err != nil {
		t.Fatalf("the metadata is not XML a reader can parse: %v\n%s", err, info)
	}
	if parsed.Title != `Tom & Jerry <"best of">` {
		t.Errorf("the title came back as %q", parsed.Title)
	}
	if parsed.PageCount != 3 || len(parsed.Pages) != 3 {
		t.Errorf("PageCount=%d, %d Page entries", parsed.PageCount, len(parsed.Pages))
	}
	// ⛔ Image is the INDEX in the archive, from zero — not the page number in
	// the document. A book made from pages 4 to 9 of something still starts at
	// zero, and a reader uses this to address the entry.
	if len(parsed.Pages) > 0 && parsed.Pages[0].Image != 0 {
		t.Errorf("the first page is Image=%d", parsed.Pages[0].Image)
	}

	// And with no title, no Title element rather than an empty one.
	out.Reset()
	if err := ToArchive(&out, pdf, RasterOptions{DPI: 36}, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<Title>") {
		t.Error("an untitled book was given an empty Title element")
	}
}

func TestWritingAnArchiveReportsWhatFailed(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	pdf, err := ToPDF([]image.Image{m}, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	if err := ToArchive(&failingWriter{}, pdf, RasterOptions{DPI: 36}, ""); err == nil {
		t.Error("a writer that refuses everything was reported as success")
	}
	if err := ToArchive(&bytes.Buffer{}, []byte("not a pdf"), RasterOptions{}, ""); err == nil {
		t.Error("something that is not a PDF was archived")
	}
}

func TestTheWidthOfAPageNumber(t *testing.T) {
	for n, want := range map[int]int{0: 1, 1: 1, 9: 1, 10: 2, 99: 2, 100: 3, 1000: 4} {
		if got := width(n); got != want {
			t.Errorf("width(%d) = %d, want %d", n, got, want)
		}
	}
}

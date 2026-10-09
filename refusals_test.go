// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/go-pdfkit/pdfkit"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, swatch(w, h)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPicturesArriveAsReadersToo(t *testing.T) {
	rs := []io.Reader{
		bytes.NewReader(pngBytes(t, 20, 10)),
		bytes.NewReader(pngBytes(t, 10, 20)),
	}
	pdf, err := ReadersToPDF(rs, Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	if boxes := mediaBoxes(t, pdf); len(boxes) != 2 {
		t.Errorf("two readers gave %d pages", len(boxes))
	}
}

func TestAReaderThatIsNotAPictureSaysWhichOneItWas(t *testing.T) {
	// ⛔ The index is the whole message. Over twenty files, "unknown format"
	// alone is a riddle rather than an error.
	rs := []io.Reader{
		bytes.NewReader(pngBytes(t, 8, 8)),
		strings.NewReader("not a picture at all"),
	}
	_, err := ReadersToPDF(rs, Options{})
	if err == nil {
		t.Fatal("a reader of rubbish was accepted")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("the refusal is %q and does not say which picture failed", err)
	}
}

func TestAPDFArrivesAsAReaderToo(t *testing.T) {
	pdf, err := ToPDF([]image.Image{swatch(16, 16)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ps, err := FromPDFReader(bytes.NewReader(pdf), RasterOptions{DPI: 36})
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Errorf("one page came back as %d", len(ps))
	}
	if _, err := FromPDFReader(failingReader{}, RasterOptions{}); err == nil {
		t.Error("a reader that cannot be read was accepted")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSomethingThatIsNotAPDFIsRefusedWhenItIsOpened(t *testing.T) {
	if _, err := FromPDF([]byte("%PDF-1.7 and then nothing useful"), RasterOptions{}); err == nil {
		t.Error("a file that is not a PDF was opened")
	}
}

func TestAnUnknownOutputFormatIsRefusedBeforeAnythingIsDrawn(t *testing.T) {
	// Cheap first. Drawing twenty pages and then discovering the format cannot
	// be written is twenty pages of work thrown away, and the error arrives
	// long after the mistake.
	pdf, err := ToPDF([]image.Image{swatch(8, 8)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromPDF(pdf, RasterOptions{Format: "nonesuch"}); err == nil {
		t.Error("an invented output format was accepted")
	}
	if _, err := FromPDF(pdf, RasterOptions{Format: "webp"}); err == nil {
		t.Error("webp was accepted as something to write")
	}
}

func TestAPasswordOnAFileThatIsNotEncryptedIsSimplyUnused(t *testing.T) {
	// ⛔ The two entry points are kept apart because OpenWithPassword("") is
	// not the same request as Open(): conflating them turns "this file needs a
	// password" into "the empty password is wrong". The other direction — a
	// password handed to a file that does not want one — is harmless, and this
	// says so rather than leaving it to be discovered.
	pdf, err := ToPDF([]image.Image{swatch(8, 8)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromPDF(pdf, RasterOptions{Password: "hunter2", DPI: 36}); err != nil {
		t.Errorf("a password on an unencrypted file was refused: %v", err)
	}
}

func TestAPictureThatIsNotThereIsNamedRatherThanDrawn(t *testing.T) {
	if _, err := ToPDF([]image.Image{swatch(4, 4), nil}, Options{}); err == nil {
		t.Error("a nil picture was laid on a page")
	}
	empty := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	if _, err := ToPDF([]image.Image{empty}, Options{}); err == nil {
		t.Error("a zero-by-zero picture was laid on a page")
	}
}

func TestATallPictureFitsByItsHeight(t *testing.T) {
	// The other half of "one scale for both axes": a wide picture is bounded
	// by the width, a tall one by the height, and only one of the two branches
	// runs for any given picture.
	_, tall := placement(100, 4000, 72, Options{Page: pdfkit.A4})
	if tall.Height > pdfkit.A4.Height+0.01 {
		t.Errorf("a very tall picture was placed %.1f points high on a %.1f page",
			tall.Height, pdfkit.A4.Height)
	}
	if got := tall.Width / tall.Height; !near(got, 0.025, 0.001) {
		t.Errorf("a 1:40 picture was placed at %.4f:1", got)
	}
}

func TestPaperGivenInPointsCanBeTurnedOnItsSide(t *testing.T) {
	p, err := ParsePageSize("200x400-landscape")
	if err != nil {
		t.Fatal(err)
	}
	if !near(p.Width, 400, 0.01) || !near(p.Height, 200, 0.01) {
		t.Errorf("200x400-landscape gave %v", p)
	}
}

func TestRubbishIsNotAPicture(t *testing.T) {
	if _, _, err := Decode(strings.NewReader("neither a PNG nor anything else")); err == nil {
		t.Error("a string was decoded as a picture")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAWriterThatFailsIsReportedRatherThanSwallowed(t *testing.T) {
	// ⛔ The reason WriteTo exists at all is that a hundred scans should not be
	// held in memory twice; the reason it is TESTABLE is the same seam. A disk
	// that fills up mid-write is the ordinary case this covers.
	err := WriteTo(failingWriter{}, []image.Image{swatch(8, 8)}, Options{})
	if err == nil {
		t.Fatal("a writer that refuses everything was reported as success")
	}
	if !strings.Contains(err.Error(), "writing") {
		t.Errorf("the refusal is %q and does not say what failed", err)
	}
}

// aPDFWithNoPages is the smallest valid PDF whose page tree is empty.
//
// ⛔ Written out by hand because nothing here can produce one: ToPDF refuses
// to. A file like this opens without complaint and holds nothing, and the
// difference between "it opened and has no pages" and "your selection matched
// nothing" is a sentence somebody will otherwise have to guess at.
const aPDFWithNoPages = `%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[]/Count 0>>endobj
xref
0 3
0000000000 65535 f 
0000000009 00000 n 
0000000056 00000 n 
trailer<</Size 3/Root 1 0 R>>
startxref
107
%%EOF
`

func TestAPDFThatOpensAndHoldsNoPagesSaysSo(t *testing.T) {
	_, err := FromPDF([]byte(aPDFWithNoPages), RasterOptions{})
	if err == nil {
		t.Fatal("a PDF of no pages came back as an empty result and no error")
	}
	if !strings.Contains(err.Error(), "no pages") {
		t.Errorf("a page-less PDF was reported as %q", err)
	}
}

func TestAnEncoderThatFailsNamesThePageAndTheFormat(t *testing.T) {
	// The encoders table is a package variable, so a failing one can be put in
	// it for the length of this test — no seam in the production code, and the
	// real error path runs.
	const name = "always-fails"
	encoders[name] = func(io.Writer, image.Image, int) error { return io.ErrShortWrite }
	t.Cleanup(func() { delete(encoders, name) })

	pdf, err := ToPDF([]image.Image{swatch(8, 8)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = FromPDF(pdf, RasterOptions{Format: name, DPI: 36})
	if err == nil {
		t.Fatal("an encoder that always fails was reported as success")
	}
	for _, want := range []string{"page 1", name} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not carry %q", err, want)
		}
	}
}

func TestAPageTooLargeToDrawIsRefusedWithItsNumber(t *testing.T) {
	// The ceiling belongs to the renderer; what this package owes is saying
	// WHICH page hit it. Over a three-hundred-page file that is the whole
	// difference between a report and a shrug.
	pdf, err := ToPDF([]image.Image{swatch(8, 8), swatch(8, 8)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = FromPDF(pdf, RasterOptions{MaxPixels: 1, DPI: 36})
	if err == nil {
		t.Fatal("a page was drawn under a one-pixel ceiling")
	}
	if !strings.Contains(err.Error(), "page 1") {
		t.Errorf("the refusal is %q and does not say which page", err)
	}
}

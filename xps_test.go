// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
	"testing"

	"github.com/go-gfx/gfx/geometry"
	"github.com/go-gfx/gfx/vector"
	"github.com/go-ooxml/opc"
	"github.com/go-pdfkit/reader"
)

const (
	ctPage = "application/vnd.ms-package.xps-fixedpage+xml"
	ctDoc  = "application/vnd.ms-package.xps-fixeddocument+xml"
	ctSeq  = "application/vnd.ms-package.xps-fixeddocumentsequence+xml"
)

// xpsOf builds the smallest package that holds one fixed page carrying the
// markup given: a sequence naming a document, a document naming the page, and
// the package relationship that points at the sequence.
func xpsOf(t *testing.T, page string) []byte {
	t.Helper()
	return xpsOfPages(t, []string{page})
}

func xpsOfPages(t *testing.T, pages []string) []byte {
	t.Helper()
	parts := []opc.NewPart{
		{Name: "/FixedDocSeq.fdseq", ContentType: ctSeq, Data: []byte(
			`<FixedDocumentSequence><DocumentReference Source="/Documents/1/FixedDoc.fdoc"/></FixedDocumentSequence>`)},
	}
	var refs strings.Builder
	for i, p := range pages {
		name := "/Documents/1/Pages/" + string(rune('1'+i)) + ".fpage"
		refs.WriteString(`<PageContent Source="` + name + `"/>`)
		parts = append(parts, opc.NewPart{Name: name, ContentType: ctPage, Data: []byte(p)})
	}
	parts = append(parts, opc.NewPart{
		Name: "/Documents/1/FixedDoc.fdoc", ContentType: ctDoc,
		Data: []byte(`<FixedDocument>` + refs.String() + `</FixedDocument>`),
	})
	b, err := opc.Build(parts, map[string][]opc.Rel{
		"": {{ID: "R1", Type: relFixedRepr, Target: "/FixedDocSeq.fdseq"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// xpsNoStartPart builds a package that has a package relationship — opc
// requires one, since "that is where every reader starts" — but NOT the fixed
// representation one. It is the fixture for the fallback: a reader that gave
// up here would report an empty document for a package whose parts say
// perfectly well what they are.
func xpsNoStartPart(t *testing.T, parts []opc.NewPart) []byte {
	t.Helper()
	b, err := opc.Build(parts, map[string][]opc.Rel{
		"": {{ID: "R1", Type: "http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail",
			Target: "/docProps/thumbnail.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// page wraps markup in a fixed page of a known size.
func xpsPage(markup string) string {
	return `<FixedPage Width="816" Height="1056" xmlns="http://schemas.microsoft.com/xps/2005/06" xml:lang="en-US">` +
		markup + `</FixedPage>`
}

// marks counts the path-painting operators on every page of the produced PDF.
//
// ⛔ IT READS THE PAGE, IT DOES NOT SEARCH THE BYTES. The first version of
// this helper counted " f\n" in the file and reported ZERO for pages that
// paint perfectly well, because pdfkit compresses its content streams — the
// helper was measuring the compressed bytes. A search that cannot see what it
// is looking for answers "not there" exactly as a correct search answers it.
//
// ⛔ And it counts OPERATORS rather than comparing pixels, because a
// rasterised page would pass a pixel comparison, and a vector converter that
// quietly rasterised is the thing this exists not to be.
func marks(t *testing.T, pdf []byte) int {
	t.Helper()
	d, err := reader.Open(pdf)
	if err != nil {
		t.Fatalf("our own reader cannot open what we wrote: %v", err)
	}
	n := 0
	for p := 1; p <= d.PageCount(); p++ {
		ops, err := d.PageOperations(p)
		if err != nil {
			t.Fatalf("page %d: %v", p, err)
		}
		for _, op := range ops {
			switch op.Operator {
			case "f", "f*", "S":
				n++
			}
		}
	}
	return n
}

// painted reports whether the PDF uses a given painting operator.
func painted(t *testing.T, pdf []byte, want string) bool {
	t.Helper()
	d, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := d.PageOperations(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Operator == want {
			return true
		}
	}
	return false
}

func TestAnXPSPageBecomesPaths(t *testing.T) {
	b := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 100,0 L 100,100 Z" Fill="#ff0000"/>`))
	pdf, rep, err := XPSToPDF(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages != 1 {
		t.Errorf("%d pages, want 1", rep.Pages)
	}
	if len(rep.Unsupported) != 0 {
		t.Errorf("nothing should have been skipped: %v", rep.Unsupported)
	}
	if n := marks(t, pdf); n == 0 {
		t.Error("the page paints nothing")
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Error("that is not a PDF")
	}
}

// TestThePageIsSizedInPointsNotXPSUnits pins the one constant that scales
// everything: ECMA-388 §10.3.1 gives page values in units of 1/96", and a PDF
// point is 1/72". 816x1056 is US Letter, so the page must come out 612x792.
//
// ⛔ Getting this wrong is invisible on screen — the drawing still fills its
// page — and wrong on paper, which is the one place a fixed-layout format is
// supposed to be right.
func TestThePageIsSizedInPointsNotXPSUnits(t *testing.T) {
	b := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,10" Stroke="#000000"/>`))
	pdf, _, err := XPSToPDF(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(pdf, []byte("612")) || !bytes.Contains(pdf, []byte("792")) {
		t.Errorf("the MediaBox is not 612x792 points; 816x1056 XPS units is US Letter")
	}
}

func TestEveryColourFormIsReadOrNamed(t *testing.T) {
	for _, tc := range []struct {
		why      string
		fill     string
		paints   bool
		skipName string
	}{
		{why: "six hex digits", fill: "#ff8000", paints: true},
		{why: "eight with alpha", fill: "#80ff8000", paints: true},
		{why: "three, each doubled", fill: "#f80", paints: true},
		{why: "four with alpha", fill: "#8f80", paints: true},
		{why: "scRGB is not read", fill: "sc#1.0,0.5,0.0", skipName: "scRGB colour"},
		{why: "a profiled colour is not read", fill: "ContextColor /p.icc 1.0,0.2", skipName: "ContextColor"},
		{why: "a named colour is not read", fill: "Red", skipName: "named colour"},
		{why: "a resource reference is not read", fill: "{StaticResource k}", skipName: "ResourceReference"},
		{why: "a length nobody writes", fill: "#ff00", paints: true}, // four digits IS valid
		{why: "an impossible length", fill: "#ff000", skipName: "colour syntax"},
		{why: "digits that are not hex", fill: "#gggggg", paints: true}, // read as zeroes, not refused
	} {
		b := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="`+tc.fill+`"/>`))
		pdf, rep, err := XPSToPDF(b, Options{})
		if err != nil {
			t.Fatalf("%s: %v", tc.why, err)
		}
		if tc.paints && marks(t, pdf) == 0 {
			t.Errorf("%s: %q painted nothing", tc.why, tc.fill)
		}
		if tc.skipName == "" {
			continue
		}
		if !contains(rep.Unsupported, tc.skipName) {
			t.Errorf("%s: %q was passed over silently; report said %v", tc.why, tc.fill, rep.Unsupported)
		}
	}
}

// TestTheFillRuleDefaultsToEvenOdd is the trap ECMA-388 Annex C sets: its
// algorithm begins "PG.FillRule = EvenOdd", so data with NO F prefix is
// even-odd — the opposite of SVG's default and of PDF's own f operator.
//
// ⛔ It shows only on a self-intersecting shape, which is why it survives a
// glance. The assertion is on the OPERATOR, f* against f, because that is
// where the difference lives.
func TestTheFillRuleDefaultsToEvenOdd(t *testing.T) {
	for _, tc := range []struct {
		why  string
		data string
		want string
	}{
		{"no prefix is even-odd", "M 0,0 L 10,0 L 10,10 Z", "f*"},
		{"F0 is even-odd", "F0 M 0,0 L 10,0 L 10,10 Z", "f*"},
		{"F1 is non-zero", "F1 M 0,0 L 10,0 L 10,10 Z", "f"},
		{"a lower-case f is the same prefix", "f1 M 0,0 L 10,0 L 10,10 Z", "f"},
	} {
		b := xpsOf(t, xpsPage(`<Path Data="`+tc.data+`" Fill="#000000"/>`))
		pdf, _, err := XPSToPDF(b, Options{})
		if err != nil {
			t.Fatalf("%s: %v", tc.why, err)
		}
		if !painted(t, pdf, tc.want) {
			t.Errorf("%s: %q did not emit %q", tc.why, tc.data, tc.want)
		}
	}
}

func TestGeometrySplitsItsPrefix(t *testing.T) {
	for _, tc := range []struct {
		in   string
		data string
		rule vector.FillRule
	}{
		{"M 0,0", "M 0,0", vector.EvenOdd},
		{"F0 M 0,0", " M 0,0", vector.EvenOdd},
		{"F1 M 0,0", " M 0,0", vector.NonZero},
		{"F", "", vector.EvenOdd},
		{"", "", vector.EvenOdd},
		{"  F1 M 0,0", " M 0,0", vector.NonZero},
	} {
		got, rule := xpsGeometry(tc.in)
		if got != tc.data || rule != tc.rule {
			t.Errorf("%q gave (%q, %v), want (%q, %v)", tc.in, got, rule, tc.data, tc.rule)
		}
	}
}

// TestARenderTransformIsNotRowMajor is the witness this fleet now keeps beside
// every affine: §14.4.1 writes m11,m12,m21,m22,dx,dy, which maps (x,y) to
// (m11*x + m21*y + dx, m12*x + m22*y + dy). Read row-major, the two middle
// numbers swap — correct for every unrotated drawing, mirrored for the rest.
//
// The expected matrix is written from the clause, not from running the code.
func TestARenderTransformIsNotRowMajor(t *testing.T) {
	se := xml.StartElement{Attr: []xml.Attr{{
		Name:  xml.Name{Local: "RenderTransform"},
		Value: "1,0.25,0.75,1,5,7",
	}}}
	m, ok := xpsTransformAttr(se, "RenderTransform")
	if !ok {
		t.Fatal("the transform was not read")
	}
	want := geometry.Matrix{Xx: 1, Yx: 0.75, X0: 5, Xy: 0.25, Yy: 1, Y0: 7}
	if m != want {
		t.Errorf("got %+v, want %+v — m12 and m21 are being read the wrong way round", m, want)
	}
	// And it moves a point the way the clause says: (4,0) goes to
	// (m11*4 + m21*0 + dx, m12*4 + m22*0 + dy) = (9, 8).
	if p := m.TransformPoint(geometry.Pt(4, 0)); p.X != 9 || p.Y != 8 {
		t.Errorf("(4,0) went to (%v,%v), want (9,8)", p.X, p.Y)
	}
}

func TestATransformThatCannotBeReadIsIgnored(t *testing.T) {
	for _, tc := range []struct{ why, v string }{
		{"absent", ""},
		{"a resource reference", "{StaticResource m}"},
		{"too few numbers", "1,0,0,1,0"},
		{"not numbers", "1,0,0,1,0,x"},
	} {
		se := xml.StartElement{Attr: []xml.Attr{{
			Name: xml.Name{Local: "RenderTransform"}, Value: tc.v,
		}}}
		if _, ok := xpsTransformAttr(se, "RenderTransform"); ok {
			t.Errorf("%s: %q was accepted", tc.why, tc.v)
		}
	}
}

func TestACanvasTransformAppliesToWhatIsInsideIt(t *testing.T) {
	// The same path, once at the origin and once pushed right by a canvas.
	plain := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/>`))
	moved := xpsOf(t, xpsPage(
		`<Canvas RenderTransform="1,0,0,1,400,0"><Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/></Canvas>`))

	a, _, err := XPSToPDF(plain, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := XPSToPDF(moved, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("the canvas transform changed nothing")
	}
}

func TestACanvasRestoresWhatItChanged(t *testing.T) {
	// A path after a closed canvas must be where it would have been without
	// one: a transform that leaks past its canvas moves every later mark.
	inside := xpsOf(t, xpsPage(
		`<Canvas RenderTransform="1,0,0,1,400,0"><Path Data="M 0,0 L 1,0 L 1,1 Z" Fill="#000000"/></Canvas>`+
			`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/>`))
	after := xpsOf(t, xpsPage(
		`<Canvas RenderTransform="1,0,0,1,400,0"><Path Data="M 0,0 L 1,0 L 1,1 Z" Fill="#000000"/></Canvas>`+
			`<Canvas><Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/></Canvas>`))
	a, _, err := XPSToPDF(inside, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := XPSToPDF(after, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("a canvas with no transform drew differently from no canvas at all")
	}
}

func TestWhatItCannotDrawIsNamed(t *testing.T) {
	for _, tc := range []struct{ markup, want string }{
		{`<Glyphs OriginX="96" OriginY="96" UnicodeString="hi" FontUri="/f.ttf" FontRenderingEmSize="16"/>`, "Glyphs"},
		{`<Path Data="M 0,0 L 1,1"><Path.Fill><ImageBrush ImageSource="/i.png"/></Path.Fill></Path>`, "ImageBrush"},
		{`<Path Data="M 0,0 L 1,1"><Path.Fill><VisualBrush/></Path.Fill></Path>`, "VisualBrush"},
		{`<Path Data="M 0,0 L 1,1"><Path.Fill><LinearGradientBrush/></Path.Fill></Path>`, "LinearGradientBrush"},
		{`<Path Data="M 0,0 L 1,1"><Path.Fill><RadialGradientBrush/></Path.Fill></Path>`, "RadialGradientBrush"},
		{`<Path><Path.Data><PathGeometry/></Path.Data></Path>`, "PathGeometry"},
		{`<Path Data="M 0,0 L 1,1" Fill="#000" Clip="M 0,0 L 5,5 Z"/>`, "Path.Clip"},
		{`<Path Data="M 0,0 L 1,1" Fill="#000" OpacityMask="#80000000"/>`, "OpacityMask"},
		{`<Canvas Clip="M 0,0 L 5,5 Z"/>`, "Canvas.Clip"},
		{`<Canvas OpacityMask="#80000000"/>`, "OpacityMask"},
		{`<Path Data="{StaticResource g}" Fill="#000"/>`, "ResourceReference"},
	} {
		_, rep, err := XPSToPDF(xpsOf(t, xpsPage(tc.markup)), Options{})
		if err != nil {
			t.Fatalf("%s: %v", tc.want, err)
		}
		if !contains(rep.Unsupported, tc.want) {
			t.Errorf("%q was not reported; got %v", tc.want, rep.Unsupported)
		}
	}
}

// TestTheReportIsSortedAndWithoutRepeats: it answers "what should I implement
// next", so a kind met forty times is one entry, and the order does not depend
// on which page happened to be read first.
func TestTheReportIsSortedAndWithoutRepeats(t *testing.T) {
	_, rep, err := XPSToPDF(xpsOf(t, xpsPage(
		`<Glyphs/><Glyphs/><Path Data="M 0,0 L 1,1"><Path.Fill><ImageBrush/></Path.Fill></Path><Glyphs/>`)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Glyphs", "ImageBrush"}
	if len(rep.Unsupported) != len(want) {
		t.Fatalf("got %v, want %v", rep.Unsupported, want)
	}
	for i := range want {
		if rep.Unsupported[i] != want[i] {
			t.Errorf("got %v, want %v", rep.Unsupported, want)
		}
	}
}

func TestAStrokeIsDrawnWithItsWidth(t *testing.T) {
	thin := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 100,0" Stroke="#000000" StrokeThickness="1"/>`))
	thick := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 100,0" Stroke="#000000" StrokeThickness="8"/>`))
	a, _, err := XPSToPDF(thin, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := XPSToPDF(thick, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("the stroke thickness changed nothing")
	}
	if !painted(t, a, "S") {
		t.Error("nothing was stroked")
	}
}

func TestAPathWithNoBrushPaintsNothing(t *testing.T) {
	// ⛔ XPS is not SVG: there is no default fill. A path with neither Fill
	// nor Stroke is a path nobody can see, and inventing black for it would
	// put ink on the page that the document does not ask for.
	pdf, rep, err := XPSToPDF(xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z"/>`)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := marks(t, pdf); n != 0 {
		t.Errorf("%d marks painted for a path with no brush", n)
	}
	if len(rep.Unsupported) != 0 {
		t.Errorf("a path with no brush is not an unsupported feature: %v", rep.Unsupported)
	}
}

func TestOpacityIsCarried(t *testing.T) {
	opaque := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/>`))
	faint := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000" Opacity="0.25"/>`))
	a, _, err := XPSToPDF(opaque, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := XPSToPDF(faint, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("Opacity changed nothing")
	}
	// An unreadable opacity is ignored rather than treated as zero, which
	// would erase the shape.
	odd := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000" Opacity="wide"/>`))
	c, _, err := XPSToPDF(odd, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, c) {
		t.Error("an unreadable Opacity was not ignored")
	}
}

func TestEveryPageOfTheDocumentIsWritten(t *testing.T) {
	b := xpsOfPages(t, []string{
		xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/>`),
		xpsPage(`<Path Data="M 0,0 L 20,0 L 20,20 Z" Fill="#000000"/>`),
		xpsPage(`<Path Data="M 0,0 L 30,0 L 30,30 Z" Fill="#000000"/>`),
	})
	_, rep, err := XPSToPDF(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages != 3 {
		t.Errorf("%d pages, want 3", rep.Pages)
	}
}

func TestAPackageWithNoStartRelationshipIsStillRead(t *testing.T) {
	// ⛔ A package whose start part cannot be found still HAS pages, and they
	// say what they are. Reporting nothing for it would be the scan that could
	// not read reporting zero.
	b := xpsNoStartPart(t, []opc.NewPart{
		{Name: "/Pages/1.fpage", ContentType: ctPage, Data: []byte(xpsPage(
			`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/>`))},
	})
	pdf, rep, err := XPSToPDF(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages != 1 || marks(t, pdf) == 0 {
		t.Errorf("%d pages, %d marks", rep.Pages, marks(t, pdf))
	}
}

func TestWhatItRefuses(t *testing.T) {
	t.Run("a file that is not a package", func(t *testing.T) {
		_, _, err := XPSToPDF([]byte("not a zip at all"), Options{})
		if !errors.Is(err, ErrNotXPS) {
			t.Errorf("%v", err)
		}
	})
	t.Run("a package with no fixed page", func(t *testing.T) {
		b := xpsNoStartPart(t, []opc.NewPart{
			{Name: "/hello.xml", ContentType: "text/xml", Data: []byte("<hello/>")},
		})
		_, _, err := XPSToPDF(b, Options{})
		if !errors.Is(err, ErrNotXPS) {
			t.Errorf("%v", err)
		}
	})
	t.Run("a part that is not a fixed page", func(t *testing.T) {
		b := xpsNoStartPart(t, []opc.NewPart{
			{Name: "/Pages/1.fpage", ContentType: ctPage, Data: []byte("<Nothing/>")},
		})
		if _, _, err := XPSToPDF(b, Options{}); err == nil {
			t.Error("a part with no FixedPage was accepted")
		}
	})
	t.Run("a page with no usable size", func(t *testing.T) {
		b := xpsNoStartPart(t, []opc.NewPart{
			{Name: "/Pages/1.fpage", ContentType: ctPage, Data: []byte(`<FixedPage Width="0" Height="0"/>`)},
		})
		if _, _, err := XPSToPDF(b, Options{}); err == nil {
			t.Error("a page with no size was accepted")
		}
	})
	t.Run("a package larger than the ceiling", func(t *testing.T) {
		_, _, err := XPSToPDF(make([]byte, maxSourceBytes+1), Options{})
		if err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Errorf("%v", err)
		}
	})
}

// TestAMarkOutsideAPageIsNotDrawn covers the guard that keeps a malformed part
// from drawing onto a page that does not exist yet.
func TestAMarkOutsideAPageIsNotDrawn(t *testing.T) {
	b := xpsNoStartPart(t, []opc.NewPart{
		{Name: "/Pages/1.fpage", ContentType: ctPage, Data: []byte(
			`<Wrapper><Path Data="M 0,0 L 1,1" Fill="#000"/>` + xpsPage(``) + `</Wrapper>`)},
	})
	pdf, _, err := XPSToPDF(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := marks(t, pdf); n != 0 {
		t.Errorf("%d marks drawn before any page began", n)
	}
}

func TestGeometryThatCannotBeParsedIsSkipped(t *testing.T) {
	pdf, rep, err := XPSToPDF(xpsOf(t, xpsPage(`<Path Data="Q is not a start" Fill="#000000"/>`)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := marks(t, pdf); n != 0 {
		t.Errorf("%d marks from unreadable geometry", n)
	}
	if len(rep.Unsupported) != 0 {
		t.Errorf("malformed data is not an unsupported FEATURE: %v", rep.Unsupported)
	}
}

// TestAPackageThatPointsAtPartsThatAreNotThere covers what a damaged or
// hand-edited package does to the walk.
//
// ⛔ EACH ONE MUST FAIL DIFFERENTLY. A page part that is named but missing is
// an error, because the document says it is there; a SEQUENCE that is missing
// only means the walk cannot start, and the parts still say what they are, so
// the content-type scan takes over. Collapsing the two would either refuse a
// readable file or silently produce a short one.
func TestAPackageThatPointsAtPartsThatAreNotThere(t *testing.T) {
	page := xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/>`)

	t.Run("the sequence is missing, so the parts are scanned instead", func(t *testing.T) {
		b, err := opc.Build([]opc.NewPart{
			{Name: "/Documents/1/Pages/1.fpage", ContentType: ctPage, Data: []byte(page)},
		}, map[string][]opc.Rel{
			"": {{ID: "R1", Type: relFixedRepr, Target: "/NotThere.fdseq"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pdf, rep, err := XPSToPDF(b, Options{})
		if err != nil {
			t.Fatalf("the page was readable and was refused: %v", err)
		}
		if rep.Pages != 1 || marks(t, pdf) == 0 {
			t.Errorf("%d pages, %d marks", rep.Pages, marks(t, pdf))
		}
	})

	t.Run("the document is missing, so the parts are scanned instead", func(t *testing.T) {
		b, err := opc.Build([]opc.NewPart{
			{Name: "/FixedDocSeq.fdseq", ContentType: ctSeq, Data: []byte(
				`<FixedDocumentSequence><DocumentReference Source="/NotThere.fdoc"/></FixedDocumentSequence>`)},
			{Name: "/Documents/1/Pages/1.fpage", ContentType: ctPage, Data: []byte(page)},
		}, map[string][]opc.Rel{
			"": {{ID: "R1", Type: relFixedRepr, Target: "/FixedDocSeq.fdseq"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, rep, err := XPSToPDF(b, Options{}); err != nil || rep.Pages != 1 {
			t.Errorf("pages=%v err=%v", rep, err)
		}
	})

	t.Run("a page that is named but absent is an error", func(t *testing.T) {
		b, err := opc.Build([]opc.NewPart{
			{Name: "/FixedDocSeq.fdseq", ContentType: ctSeq, Data: []byte(
				`<FixedDocumentSequence><DocumentReference Source="/Doc.fdoc"/></FixedDocumentSequence>`)},
			{Name: "/Doc.fdoc", ContentType: ctDoc, Data: []byte(
				`<FixedDocument><PageContent Source="/Gone.fpage"/></FixedDocument>`)},
		}, map[string][]opc.Rel{
			"": {{ID: "R1", Type: relFixedRepr, Target: "/FixedDocSeq.fdseq"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := XPSToPDF(b, Options{}); err == nil {
			t.Error("a page the document names and the package does not hold was accepted")
		}
	})

	t.Run("a Source that resolves to nothing is passed over", func(t *testing.T) {
		// An empty Source, and one that climbs out of the package: neither
		// names a part, and a reader that joined them blindly would ask for
		// something outside the file. The page that IS named still arrives.
		b, err := opc.Build([]opc.NewPart{
			{Name: "/FixedDocSeq.fdseq", ContentType: ctSeq, Data: []byte(
				`<FixedDocumentSequence><DocumentReference Source="/Doc.fdoc"/></FixedDocumentSequence>`)},
			{Name: "/Doc.fdoc", ContentType: ctDoc, Data: []byte(
				`<FixedDocument><PageContent Source=""/><PageContent Source="../../outside.fpage"/>` +
					`<PageContent Source="/P.fpage"/></FixedDocument>`)},
			{Name: "/P.fpage", ContentType: ctPage, Data: []byte(page)},
		}, map[string][]opc.Rel{
			"": {{ID: "R1", Type: relFixedRepr, Target: "/FixedDocSeq.fdseq"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, rep, err := XPSToPDF(b, Options{})
		if err != nil {
			t.Fatalf("the one real page was refused: %v", err)
		}
		if rep.Pages != 1 {
			t.Errorf("%d pages, want the 1 that exists", rep.Pages)
		}
	})

	t.Run("an attribute that is not Source is passed over", func(t *testing.T) {
		b, err := opc.Build([]opc.NewPart{
			{Name: "/FixedDocSeq.fdseq", ContentType: ctSeq, Data: []byte(
				`<FixedDocumentSequence><DocumentReference x:Key="k" Source="/Doc.fdoc" xmlns:x="urn:x"/></FixedDocumentSequence>`)},
			{Name: "/Doc.fdoc", ContentType: ctDoc, Data: []byte(
				`<FixedDocument><PageContent Width="1" Source="/P.fpage"/></FixedDocument>`)},
			{Name: "/P.fpage", ContentType: ctPage, Data: []byte(page)},
		}, map[string][]opc.Rel{
			"": {{ID: "R1", Type: relFixedRepr, Target: "/FixedDocSeq.fdseq"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, rep, err := XPSToPDF(b, Options{}); err != nil || rep.Pages != 1 {
			t.Errorf("pages=%v err=%v", rep, err)
		}
	})
}

// TestAPathCarriesItsOwnTransform: §14.4.3 lets a Path place itself, on top of
// whatever canvases enclose it.
func TestAPathCarriesItsOwnTransform(t *testing.T) {
	plain := xpsOf(t, xpsPage(`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000"/>`))
	moved := xpsOf(t, xpsPage(
		`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#000000" RenderTransform="1,0,0,1,300,0"/>`))
	a, _, err := XPSToPDF(plain, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := XPSToPDF(moved, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("the path's own RenderTransform changed nothing")
	}
}

func TestAPageWhoseSizeIsNotANumber(t *testing.T) {
	b := xpsNoStartPart(t, []opc.NewPart{
		{Name: "/P.fpage", ContentType: ctPage, Data: []byte(
			`<FixedPage Width="wide" Height="1056"/>`)},
	})
	if _, _, err := XPSToPDF(b, Options{}); err == nil {
		t.Error(`Width="wide" was accepted`)
	}
}

func TestAThreeDigitColourThatIsNotHex(t *testing.T) {
	// The short form goes through a different reader from the long one, so
	// "#ggg" and "#gggggg" are two cases, not one.
	pdf, rep, err := XPSToPDF(xpsOf(t, xpsPage(
		`<Path Data="M 0,0 L 10,0 L 10,10 Z" Fill="#ggg"/>`)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if marks(t, pdf) == 0 {
		t.Error("an unreadable short colour should still paint, as black")
	}
	if len(rep.Unsupported) != 0 {
		t.Errorf("bad hex is not an unsupported feature: %v", rep.Unsupported)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

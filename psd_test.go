// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"slices"
	"strings"
	"testing"
)

// psdFixture is the smallest Photoshop file there is: a 2×2 RGB composite,
// uncompressed. It is built here rather than committed, because a .psd from
// anywhere else is somebody's artwork.
//
// ⛔ PLANAR, which is the whole shape of the format: all of the red, then all
// of the green, then all of the blue.
func psdFixture() []byte {
	var b bytes.Buffer
	b.WriteString("8BPS")
	be16(&b, 1)              // version: PSD
	b.Write(make([]byte, 6)) // reserved
	be16(&b, 3)              // channels
	be32(&b, 2)              // height
	be32(&b, 2)              // width
	be16(&b, 8)              // depth
	be16(&b, 3)              // colour mode: RGB
	be32(&b, 0)              // colour mode data
	be32(&b, 0)              // image resources
	be32(&b, 0)              // layer and mask information
	be16(&b, 0)              // compression: raw
	b.Write([]byte{0xff, 0x00, 0x00, 0xff})
	b.Write([]byte{0x00, 0xff, 0x00, 0xff})
	b.Write([]byte{0x00, 0x00, 0xff, 0x00})
	return b.Bytes()
}

func be16(b *bytes.Buffer, v int) { _ = binary.Write(b, binary.BigEndian, uint16(v)) }
func be32(b *bytes.Buffer, v int) { _ = binary.Write(b, binary.BigEndian, uint32(v)) }

// ⛔ The capability BentoPDF calls psd-to-pdf, end to end: a Photoshop file in,
// a PDF out. What makes it work is one import — this package decodes through
// image.Decode, so go-images/psd registering itself is the whole wiring.
func TestAPhotoshopFileBecomesAPDF(t *testing.T) {
	m, format, err := DecodeBytes(psdFixture())
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	if format != "psd" {
		t.Fatalf("format = %q, want psd", format)
	}
	if got := m.Bounds(); got.Dx() != 2 || got.Dy() != 2 {
		t.Errorf("bounds = %v, want 2x2", got)
	}
	// The planes are not interleaved: the first pixel is red, the last yellow.
	if got, want := m.At(0, 0), (color.NRGBA{R: 0xff, A: 0xff}); got != color.Color(want) {
		t.Errorf("(0,0) = %v, want %v", got, want)
	}
	if got, want := m.At(1, 1), (color.NRGBA{R: 0xff, G: 0xff, A: 0xff}); got != color.Color(want) {
		t.Errorf("(1,1) = %v, want %v", got, want)
	}

	pdf, err := ToPDF([]image.Image{m}, Options{})
	if err != nil {
		t.Fatalf("ToPDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("what came back is not a PDF: %.16q", pdf)
	}
	// ⛔ One page, measured by its /MediaBox rather than by searching for the
	// page object: the writer may put that object in a compressed stream,
	// where a grep for "/Type /Page" finds nothing and reports a PDF with no
	// pages — which is what the first version of this assertion did.
	boxes := mediaBoxes(t, pdf)
	if len(boxes) != 1 {
		t.Fatalf("%d pages, want one for one picture", len(boxes))
	}
	if w, h := boxes[0][0], boxes[0][1]; w <= 0 || h <= 0 {
		t.Errorf("the page measures %g by %g points", w, h)
	}
}

// ⛔ The read list is DERIVED from the tables, because it used to be typed out
// in cmd/pdfconv and would have gone on saying six formats on the day a
// seventh started working.
func TestReaders(t *testing.T) {
	got := Readers()
	if !slices.IsSorted(got) {
		t.Errorf("Readers() = %v, want it sorted so the output is stable", got)
	}
	for _, want := range []string{"png", "jpeg", "gif", "bmp", "tiff", "webp", "psd"} {
		if !slices.Contains(got, want) {
			t.Errorf("Readers() does not name %q: %v", want, got)
		}
	}
	// Everything this package writes, it can also read.
	for _, w := range Encoders() {
		if !slices.Contains(got, w) {
			t.Errorf("%q can be written and is not in Readers(): %v", w, got)
		}
	}
	// And the two formats it reads and cannot write are named as such.
	if _, err := encoderFor("psd"); err == nil {
		t.Error("psd was accepted as something to write")
	} else if !strings.Contains(err.Error(), "can be read here but not written") {
		t.Errorf("psd was refused as %q, which reads like an unknown format", err)
	}
}

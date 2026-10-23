// Copyright (c) 2026, the go-pdfkit authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"

	xwebp "golang.org/x/image/webp"
)

// ⛔ THE PAGE IS DECODED AGAIN, not merely counted. "FromPDF returned some
// bytes and called them webp" is true of an encoder that wrote nothing of the
// sort; reading them back with a decoder nobody here wrote is what says the
// file is a WebP at all.
func TestAPageComesOutAsAReadableWebP(t *testing.T) {
	pdf, err := ToPDF([]image.Image{swatch(64, 48)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []int{40, 80, 100} {
		pages, err := FromPDF(pdf, RasterOptions{Format: "webp", Quality: q, DPI: 72})
		if err != nil {
			t.Fatalf("quality %d: %v", q, err)
		}
		if len(pages) != 1 {
			t.Fatalf("quality %d: %d pages", q, len(pages))
		}
		if pages[0].Format != "webp" {
			t.Errorf("quality %d: the page says it is %q", q, pages[0].Format)
		}
		m, err := xwebp.Decode(bytes.NewReader(pages[0].Data))
		if err != nil {
			t.Fatalf("quality %d: the page is not a readable WebP: %v", q, err)
		}
		if m.Bounds().Dx() == 0 || m.Bounds().Dy() == 0 {
			t.Errorf("quality %d: the page decoded to %v", q, m.Bounds())
		}
	}
}

// ⛔ 100 IS A DIFFERENT CODEC, not the finest setting of the same one. The
// container says which: a lossless file carries a 'VP8L' chunk and a lossy one
// carries 'VP8 ', with a trailing space. Checking the picture instead would
// not tell them apart — a high-quality lossy page looks like the original too.
func TestQualityOneHundredChoosesTheLosslessCodec(t *testing.T) {
	pdf, err := ToPDF([]image.Image{swatch(32, 32)}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		quality int
		want    string
	}{
		{100, "VP8L"},
		{90, "VP8 "},
		{10, "VP8 "},
	} {
		pages, err := FromPDF(pdf, RasterOptions{Format: "webp", Quality: c.quality, DPI: 72})
		if err != nil {
			t.Fatalf("quality %d: %v", c.quality, err)
		}
		b := pages[0].Data
		if len(b) < 16 {
			t.Fatalf("quality %d: %d bytes", c.quality, len(b))
		}
		if got := string(b[12:16]); got != c.want {
			t.Errorf("quality %d produced a %q chunk, wanted %q", c.quality, got, c.want)
		}
	}
}

// ⛔ WHICH CODEC WINS DEPENDS ON THE PAGE, and the first version of this test
// asserted the wrong one. On a flat swatch the lossless form came out at 102
// bytes and the lossy one at 176: a VP8 frame carries a fixed header — 1 056
// bits of "every coefficient probability is unchanged" alone — that a picture
// with almost nothing in it can never pay back.
//
// That is the honest shape of the choice, and the reason quality 100 reaches
// the other codec rather than the finest setting of this one. The lossy form
// earns its place on a DETAILED page, which is what this measures.
func TestTheLossyPageIsSmallerOnADetailedPage(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 160, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 160; x++ {
			v := 128 + 90*math.Sin(float64(x)/5.5) + 50*math.Cos(float64(y)/3.5)
			m.Set(x, y, color.RGBA{byte(clampTo255(v)), byte(clampTo255(v * 0.7)),
				byte(clampTo255(v*0.5 + 60)), 255})
		}
	}
	pdf, err := ToPDF([]image.Image{m}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lossless, err := FromPDF(pdf, RasterOptions{Format: "webp", Quality: 100, DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	lossy, err := FromPDF(pdf, RasterOptions{Format: "webp", Quality: 50, DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("a detailed page: lossless %d bytes, lossy at 50 %d bytes",
		len(lossless[0].Data), len(lossy[0].Data))
	if len(lossy[0].Data) >= len(lossless[0].Data) {
		t.Errorf("the lossy page is %d bytes and the lossless one %d, so the quality "+
			"setting earns nothing on the content it exists for",
			len(lossy[0].Data), len(lossless[0].Data))
	}
}

func clampTo255(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// webp is listed among the formats that can be written, so that anything
// asking what it can produce finds it.
func TestWebPIsListedAmongTheEncoders(t *testing.T) {
	found := false
	for _, n := range Encoders() {
		if n == "webp" {
			found = true
		}
	}
	if !found {
		t.Errorf("webp is not in %v", Encoders())
	}
	// And still among the readers, which it always was.
	found = false
	for _, n := range Readers() {
		if n == "webp" {
			found = true
		}
	}
	if !found {
		t.Errorf("webp is not in %v", Readers())
	}
}

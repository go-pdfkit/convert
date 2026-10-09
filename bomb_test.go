// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"runtime"
	"strings"
	"testing"
)

// claimingPNG is a real one-pixel PNG whose IHDR has been rewritten to claim
// w by h, with the chunk's CRC recomputed so every decoder accepts the header.
// The pixel data is still one pixel.
//
// ⛔ This is what a decompression bomb actually looks like: it is not large,
// it is not malformed, and nothing about it is suspicious until something
// multiplies its two numbers together.
func claimingPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// 8 bytes of signature, then the IHDR chunk: 4 length, 4 type, 13 data,
	// 4 CRC. Width and height are the first two of those thirteen.
	const ihdrType = 12
	const ihdrData = 16
	put32(b[ihdrData:], w)
	put32(b[ihdrData+4:], h)
	put32(b[ihdrData+13:], crc32.ChecksumIEEE(b[ihdrType:ihdrData+13]))
	return b
}

func put32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}

// heldDuring is how much the heap grew across f, after a collection either
// side. It is coarse, which is all this needs: the difference being measured
// is between megabytes and hundreds of them.
func heldDuring(f func()) float64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc < before.HeapAlloc {
		return 0
	}
	return float64(after.HeapAlloc-before.HeapAlloc) / (1 << 20)
}

func TestAPictureIsRefusedBeforeItsPixelsAreAllocated(t *testing.T) {
	// ⛔ The defect this file exists for, and the reason it is measured rather
	// than read. The ceiling WAS there — in ToPDF, after image.Decode had
	// returned. A check that runs after the allocation it bounds reports a
	// buffer the process is already holding.
	//
	// Measured before the fix: 381.6 MiB held for a 75-byte input, with
	// MaxPixels: 1000 set.
	bomb := claimingPNG(t, 10000, 10000)
	if len(bomb) > 200 {
		t.Fatalf("the fixture is %d bytes, which is not a bomb", len(bomb))
	}

	var err error
	held := heldDuring(func() {
		_, err = ReadersToPDF([]io.Reader{bytes.NewReader(bomb)}, Options{MaxPixels: 1000})
	})
	if err == nil {
		t.Fatal("a picture claiming a hundred million pixels was accepted under a ceiling of a thousand")
	}
	// ⛔ The refusal has to be about the SIZE. "not enough image data" is what
	// came back before, and it arrived from deep inside the decoder — after
	// the allocation, and saying nothing about why the file was unacceptable.
	if !strings.Contains(err.Error(), "pixels") {
		t.Errorf("it was refused as %q, which is not a refusal about its size", err)
	}
	// Thirty-two megabytes is far above what reading a 75-byte header costs
	// and far below the 381.6 MiB the defect produced, so this distinguishes
	// the two without being a measurement of the garbage collector.
	if held > 32 {
		t.Errorf("refusing a 75-byte picture held %.1f MiB: the ceiling is still being "+
			"applied after the allocation it is meant to bound", held)
	}
}

func TestTheSameBombThroughTheDefaultCeiling(t *testing.T) {
	// A caller who sets nothing is still covered: Decode uses DefaultMaxPixels.
	bomb := claimingPNG(t, 70000, 70000) // 4.9e9 pixels
	_, _, err := DecodeBytes(bomb)
	if err == nil {
		t.Fatal("a picture of nearly five billion pixels was accepted by default")
	}
	if !strings.Contains(err.Error(), "pixels") {
		t.Errorf("it was refused as %q", err)
	}
}

func TestTheDefaultCeilingIsWhereItSaysItIs(t *testing.T) {
	// ⛔ Both sides of the boundary, because an audit probe built at exactly
	// 10000x10000 was allowed — correctly, that being exactly the default —
	// and it read for a moment like the guard had not fired.
	//
	// Nothing is decoded here: the header alone decides, which is the whole
	// change, so asserting at the boundary costs no memory at all.
	if _, _, err := DecodeBytes(claimingPNG(t, 10000, 10000)); err != nil &&
		strings.Contains(err.Error(), "pixels") {
		t.Errorf("exactly DefaultMaxPixels was refused as too large: %v", err)
	}
	_, _, err := DecodeBytes(claimingPNG(t, 10001, 10000))
	if err == nil || !strings.Contains(err.Error(), "pixels") {
		t.Errorf("one row past DefaultMaxPixels gave %v", err)
	}
}

func TestAPictureUnderTheCeilingStillDecodes(t *testing.T) {
	// ⛔ The side a ceiling gets wrong. Tested only from above, a limit is
	// satisfied by refusing everything.
	var buf bytes.Buffer
	if err := png.Encode(&buf, swatch(400, 300)); err != nil {
		t.Fatal(err)
	}
	m, format, err := DecodeBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("an ordinary picture was refused: %v", err)
	}
	if format != "png" || m.Bounds().Dx() != 400 {
		t.Errorf("it came back as %s, %v", format, m.Bounds())
	}
	// And exactly at the ceiling, through the door that takes one.
	if _, _, err := decodeWithin(bytes.NewReader(buf.Bytes()), 400*300); err != nil {
		t.Errorf("a picture exactly at the ceiling was refused: %v", err)
	}
	if _, _, err := decodeWithin(bytes.NewReader(buf.Bytes()), 400*300-1); err == nil {
		t.Error("a picture one pixel past the ceiling was accepted")
	}
}

func TestAnEncodedFileLargerThanAllowedIsRefused(t *testing.T) {
	// The other half: the ceiling above is on PIXELS, and a file can be large
	// without claiming many. Reading the header needs the bytes held, so what
	// is held is bounded too.
	_, _, err := Decode(bytes.NewReader(make([]byte, maxSourceBytes+1)))
	if err == nil {
		t.Fatal("a file past the source ceiling was read")
	}
	if !strings.Contains(err.Error(), "bytes allowed") {
		t.Errorf("it was refused as %q", err)
	}
}

func TestAReaderThatFailsPartWayIsReported(t *testing.T) {
	// ⛔ Holding the bytes is what lets the header be read before the pixels
	// are allocated — so a read that fails is now a failure mode this package
	// has, where before it was the decoder's business. A truncated upload or a
	// disconnected network file is the ordinary case.
	if _, _, err := Decode(failingReader{}); err == nil {
		t.Fatal("a reader that refuses everything was accepted")
	}
}

func TestAZeroCeilingMeansTheDefaultAndNotNoPicturesAtAll(t *testing.T) {
	// ⛔ Options{} is the ordinary case: a caller who said nothing about size.
	// Reading zero as "no pixels allowed" would refuse every picture, and the
	// refusal would be about a limit nobody set.
	var buf bytes.Buffer
	if err := png.Encode(&buf, swatch(8, 8)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadersToPDF([]io.Reader{bytes.NewReader(buf.Bytes())}, Options{}); err != nil {
		t.Errorf("a picture was refused when no ceiling was given: %v", err)
	}
}

// A decoder that lies. Every codec in the standard library tells the truth
// about its own size, so the guard against one that does not cannot be reached
// through them — and a guard nothing can reach is a guard nobody has checked.
//
// ⛔ This is not a contrivance. image.Decode consults a REGISTRY, and anything
// linked into the program may add to it: a third-party WebP, HEIC or JPEG 2000
// reader is exactly a decoder this package did not write, whose DecodeConfig
// and whose Decode are two different functions that nothing forces to agree.
const (
	liarMagic  = "GOPDFKIT-TEST-LIAR"
	liarFormat = "liar"
)

func init() {
	image.RegisterFormat(liarFormat, liarMagic,
		func(r io.Reader) (image.Image, error) {
			// Says one pixel in the header, hands back a million. Kept at a
			// million rather than something spectacular because the test must
			// hold what it produces: four megabytes, not four hundred.
			return image.NewNRGBA(image.Rect(0, 0, 1000, 1000)), nil
		},
		func(r io.Reader) (image.Config, error) {
			return image.Config{ColorModel: nil, Width: 1, Height: 1}, nil
		})
}

func TestADecoderThatLiesAboutItsSizeIsCaughtOnTheWayOut(t *testing.T) {
	// A ceiling of a hundred: the header's 1x1 sails through it, and the
	// body's million does not.
	_, _, err := decodeWithin(bytes.NewReader([]byte(liarMagic+"whatever follows")), 100)
	if err == nil {
		t.Fatal("a decoder that said 1x1 and produced 1000x1000 was accepted under a ceiling of 100")
	}
	if !strings.Contains(err.Error(), "decoded to") {
		t.Errorf("it was refused as %q, which is not the refusal about what came BACK", err)
	}
	// And under a ceiling the body also fits: accepted, so the guard is about
	// the SIZE rather than about disagreement for its own sake.
	if _, _, err := decodeWithin(bytes.NewReader([]byte(liarMagic+"x")), 1000*1000); err != nil {
		t.Errorf("a picture whose body fits the ceiling was refused: %v", err)
	}
}

func TestAHeaderThatDisagreesWithItsBodyIsRefused(t *testing.T) {
	// ⛔ The header is a CLAIM, and the ceiling reads it. A file whose body
	// decodes to something else would walk straight past a check that only
	// ever asked the header — so what came back is measured as well.
	//
	// A PNG that claims a size its pixel data cannot fill is the reachable
	// case: the header passes the ceiling, and the decode then fails. What
	// matters is that it is refused rather than returned half-built.
	bomb := claimingPNG(t, 4000, 4000) // under the default ceiling
	m, _, err := DecodeBytes(bomb)
	if err == nil {
		t.Fatalf("a PNG whose body cannot fill its header came back as %v", m.Bounds())
	}
}

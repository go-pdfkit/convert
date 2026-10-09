// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"sort"
	"strings"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"

	// Registered for their side effect: image.Decode consults this registry,
	// and WebP has a decoder and no encoder.
	_ "golang.org/x/image/webp"
)

// Encoder is a picture format this package can WRITE. Reading is wider than
// writing — WebP decodes and does not encode — so the two are separate tables
// rather than one list with exceptions in it.
type Encoder func(w io.Writer, m image.Image, quality int) error

var encoders = map[string]Encoder{
	"png": func(w io.Writer, m image.Image, _ int) error {
		return png.Encode(w, m)
	},
	"jpeg": func(w io.Writer, m image.Image, q int) error {
		return jpeg.Encode(w, m, &jpeg.Options{Quality: q})
	},
	"gif": func(w io.Writer, m image.Image, _ int) error {
		return gif.Encode(w, m, nil)
	},
	"bmp": func(w io.Writer, m image.Image, _ int) error {
		return bmp.Encode(w, m)
	},
	"tiff": func(w io.Writer, m image.Image, _ int) error {
		return tiff.Encode(w, m, nil)
	},
}

// aliases are the names people type. "jpg" is the same format as "jpeg" and
// nobody should have to know which spelling this package chose.
var aliases = map[string]string{
	"jpg":  "jpeg",
	"tif":  "tiff",
	"jpe":  "jpeg",
	"jfif": "jpeg",
}

// decodeOnly are formats this package READS and cannot write. Naming them is
// the point: a request for one has to be refused BY NAME.
//
// ⛔ There is no WebP encoder in pure Go. Without this table the lookup in
// [Encoders] would simply miss, and "webp is not a format I know" is a
// different and misleading answer from "webp can be read here but not written".
var decodeOnly = map[string]bool{"webp": true}

// canonical resolves a format name the way a person typed it.
func canonical(name string) string {
	n := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "."))
	if a, ok := aliases[n]; ok {
		return a
	}
	return n
}

// Encoders names every format [FromPDF] can write, in a stable order.
func Encoders() []string {
	out := make([]string, 0, len(encoders))
	for k := range encoders {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// encoderFor resolves a format name, and refuses the two cases differently:
// one this package reads but cannot write, and one it does not know at all.
func encoderFor(name string) (Encoder, error) {
	n := canonical(name)
	if e, ok := encoders[n]; ok {
		return e, nil
	}
	if decodeOnly[n] {
		return nil, fmt.Errorf("%s can be read here but not written: there is no %s encoder in pure Go, "+
			"and writing a different format under that name would be worse than refusing", n, n)
	}
	return nil, fmt.Errorf("unknown picture format %q: this package writes %s",
		name, strings.Join(Encoders(), ", "))
}

// Decode reads a picture and says what format it turned out to be.
//
// ⛔ It reads the CONTENT. A file's name is a claim its bytes do not have to
// honour: a scanner that writes JPEG into "page.png" is ordinary, and a
// converter that believed the extension would hand the encoder the wrong
// picture or refuse a file it can read perfectly well.
// It refuses a picture of more than [DefaultMaxPixels] pixels, and it refuses
// it by its HEADER — see decodeWithin for why that distinction is the guard.
func Decode(r io.Reader) (image.Image, string, error) {
	return decodeWithin(r, DefaultMaxPixels)
}

// DecodeBytes is Decode over a slice.
func DecodeBytes(b []byte) (image.Image, string, error) {
	return Decode(bytes.NewReader(b))
}

// DefaultMaxPixels is how large a picture [Decode] will read when no ceiling
// is given. A hundred megapixels is A4 at a thousand dots to the inch.
//
// ⛔ Said in bytes, because "a hundred megapixels" does not tell anybody what
// it permits: a decoded picture is four bytes a pixel, so this default lets a
// file of a few dozen bytes cost FOUR HUNDRED MEGABYTES. That is bounded,
// which is the property that matters and the one this package did not have —
// but a service taking pictures from strangers should set
// [Options.MaxPixels] to what its own pages actually need rather than inherit
// this.
const DefaultMaxPixels = 100_000_000

// maxSourceBytes is how much ENCODED input is held while the header is read.
// It bounds the file on disk, not the picture inside it, and is generous
// because a raw TIFF scan is legitimately large.
const maxSourceBytes = 256 << 20

// decodeWithin reads a picture, refusing one larger than maxPixels.
//
// ⛔ It asks the HEADER first, through image.DecodeConfig, and that is the
// whole guard. The first version of this package checked the size after
// image.Decode returned — which is after the pixel buffer has been allocated,
// so the check could only ever report a picture it had already held. Measured:
// a seventy-five byte PNG whose IHDR claims 10000x10000 made the process
// allocate 381.6 MiB, and passing MaxPixels: 1000 changed nothing at all. A
// ceiling enforced after the allocation it bounds is not a ceiling; it is a
// comment.
//
// Reading the header needs the bytes twice, so the encoded input is held. That
// is bounded by maxSourceBytes and by what the picture costs ON DISK — which
// an attacker cannot inflate, inflation being exactly what the pixel ceiling
// now refuses.
func decodeWithin(r io.Reader, maxPixels int) (image.Image, string, error) {
	if maxPixels <= 0 {
		maxPixels = DefaultMaxPixels
	}
	b, err := io.ReadAll(io.LimitReader(r, maxSourceBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading a picture: %w", err)
	}
	if len(b) > maxSourceBytes {
		return nil, "", fmt.Errorf("reading a picture: it is larger than the %d bytes allowed",
			maxSourceBytes)
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, "", fmt.Errorf("reading a picture: %w", err)
	}
	// ⛔ Multiplied in a type that cannot wrap on any platform this builds for.
	// Two numbers a file chose, multiplied in int on a 32-bit build, is how a
	// ceiling gets passed by overflowing past it.
	if px := int64(cfg.Width) * int64(cfg.Height); px > int64(maxPixels) {
		return nil, "", fmt.Errorf("reading a picture: it says it is %dx%d (%d pixels), "+
			"past the %d allowed", cfg.Width, cfg.Height, px, maxPixels)
	}

	// ⛔ The format is NOT checked again here. image.DecodeConfig and
	// image.Decode match against the same registry by the same magic, so they
	// cannot disagree — a first draft compared them, and the mutation removing
	// that comparison survived, which is how an unreachable guard announces
	// itself. A check that cannot fire is not defence in depth; it is a claim
	// nobody has tested.
	m, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, "", fmt.Errorf("reading a picture: %w", err)
	}
	// ⛔ The header is a CLAIM. A decoder that produced something other than
	// the size advertised would walk straight past a ceiling that only ever
	// read the header, so what came back is measured too.
	if bb := m.Bounds(); int64(bb.Dx())*int64(bb.Dy()) > int64(maxPixels) {
		return nil, "", fmt.Errorf("reading a picture: it said it was %dx%d and decoded to "+
			"%dx%d, past the %d pixels allowed", cfg.Width, cfg.Height, bb.Dx(), bb.Dy(), maxPixels)
	}
	return m, format, nil
}

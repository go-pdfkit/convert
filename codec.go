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
func Decode(r io.Reader) (image.Image, string, error) {
	m, format, err := image.Decode(r)
	if err != nil {
		return nil, "", fmt.Errorf("reading a picture: %w", err)
	}
	return m, format, nil
}

// DecodeBytes is Decode over a slice.
func DecodeBytes(b []byte) (image.Image, string, error) {
	return Decode(bytes.NewReader(b))
}

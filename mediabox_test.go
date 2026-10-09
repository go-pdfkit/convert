// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"regexp"
	"strconv"
	"testing"
)

// mediaBoxes reads every /MediaBox off the raw PDF.
//
// ⛔ Deliberately NOT through go-pdfkit/reader. The file was written by
// go-pdfkit/pdfkit and this package's own round trip already draws it with
// go-pdfkit/render: a size read back through the same family's reader would
// agree with the writer about a convention they share, and a test where the
// judge and the subject are the same code proves they are consistent, not that
// they are right. Five numbers in a regular expression are something a person
// can check against the specification.
var mediaBoxRe = regexp.MustCompile(
	`/MediaBox\s*\[\s*(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s+(-?[\d.]+)\s*\]`)

func mediaBoxes(t *testing.T, pdf []byte) [][2]float64 {
	t.Helper()
	ms := mediaBoxRe.FindAllSubmatch(pdf, -1)
	if len(ms) == 0 {
		t.Fatal("the PDF carries no /MediaBox at all, so this test can see nothing")
	}
	out := make([][2]float64, 0, len(ms))
	for _, m := range ms {
		n := make([]float64, 4)
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseFloat(string(m[i+1]), 64)
			if err != nil {
				t.Fatalf("a /MediaBox holds %q", m[i+1])
			}
			n[i] = v
		}
		out = append(out, [2]float64{n[2] - n[0], n[3] - n[1]})
	}
	return out
}

func mediaBox(t *testing.T, pdf []byte) (float64, float64) {
	t.Helper()
	b := mediaBoxes(t, pdf)
	return b[0][0], b[0][1]
}

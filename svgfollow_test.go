// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"io"
	"math"
	"strings"
	"testing"
)

// a.sum+len(data) wraps negative on a 32-bit int for a slice near 2 GiB, and a
// wrapped sum passes "> MaxSVGBytes". The same wrap is reached on any width by
// starting the sum near the top of int.
func TestSVGArchiveAddRefusesASumThatWouldWrap(t *testing.T) {
	a := NewSVGArchive(io.Discard)
	a.sum = math.MaxInt - 10
	err := a.Add(1, make([]byte, 100))
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("a sum that wraps was accepted: %v", err)
	}
	if len(a.seen) != 0 {
		t.Error("a refused page was recorded")
	}
	// And the boundary itself: exactly what is left fits, one more does not.
	b := NewSVGArchive(io.Discard)
	b.sum = MaxSVGBytes - 3
	if err := b.Add(1, []byte("abc")); err != nil {
		t.Errorf("exactly the remainder was refused: %v", err)
	}
	if err := b.Add(2, []byte("d")); err == nil {
		t.Error("a byte past the limit was accepted")
	}
}

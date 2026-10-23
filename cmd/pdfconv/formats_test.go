// Copyright (c) 2026, the go-pdfkit authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pdfkit/convert"
)

// ⛔ A LIST OF FORMATS TYPED INTO A FLAG DESCRIPTION IS A SNAPSHOT OF WHAT
// SOMEBODY BELIEVED THE DAY THEY TYPED IT. Two of them here went on saying
// "png, jpeg, gif, bmp or tiff" after WebP became writable, and nothing
// failed: a help string is prose, and prose compiles whatever it says.
//
// This asks the package what it can write and holds the descriptions to it.
// The same reasoning as the `formats` subcommand, which derives its answer
// rather than reciting one.
func TestEveryWritableFormatIsNamedInTheFlags(t *testing.T) {
	for _, verb := range []string{"from-pdf", "to-archive"} {
		var out, errw bytes.Buffer
		// A flag set with nothing to do prints its usage to the error writer.
		_ = runTo([]string{verb, "-h"}, &out, &errw)
		help := out.String() + errw.String()
		if !strings.Contains(help, "format") {
			t.Fatalf("%s: the help does not mention -format at all:\n%s", verb, help)
		}
		for _, name := range convert.Encoders() {
			if !strings.Contains(help, name) {
				t.Errorf("%s: %q can be written and the flags never name it:\n%s",
					verb, name, help)
			}
		}
	}
}

// And the quality flag has to say what it governs, now that it governs two
// formats rather than one — and that its top value changes the codec.
func TestTheQualityFlagSaysWhatItGoverns(t *testing.T) {
	for _, verb := range []string{"from-pdf", "to-archive"} {
		var out, errw bytes.Buffer
		_ = runTo([]string{verb, "-h"}, &out, &errw)
		help := strings.ToLower(out.String() + errw.String())
		for _, want := range []string{"jpeg", "webp", "lossless"} {
			if !strings.Contains(help, want) {
				t.Errorf("%s: the quality flag's description never says %q:\n%s",
					verb, want, help)
			}
		}
	}
}

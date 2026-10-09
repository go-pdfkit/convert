// Copyright (c) the go-pdfkit authors.
// SPDX-License-Identifier: BSD-3-Clause

package convert

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-pdfkit/pdfkit"
)

// named is the paper a person can ask for by name.
var named = map[string]pdfkit.PageSize{
	"a3":      pdfkit.A3,
	"a4":      pdfkit.A4,
	"a5":      pdfkit.A5,
	"letter":  pdfkit.Letter,
	"legal":   pdfkit.Legal,
	"tabloid": pdfkit.Tabloid,
}

// PageSizes names every paper size this package knows, in a stable order.
func PageSizes() []string {
	out := make([]string, 0, len(named))
	for k := range named {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ParsePageSize reads a paper size: a name such as "a4", optionally with
// "-landscape", or "WIDTHxHEIGHT" in points.
//
// ⛔ "" is NOT an error and NOT a default size: it means "size each page to its
// own picture", which is a different instruction from "use A4". A set of scans
// of different sizes must not all be forced onto one paper because the caller
// said nothing.
func ParsePageSize(s string) (pdfkit.PageSize, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return pdfkit.PageSize{}, nil
	}
	landscape := false
	if rest, ok := strings.CutSuffix(s, "-landscape"); ok {
		s, landscape = rest, true
	}
	if p, ok := named[s]; ok {
		if landscape {
			p.Width, p.Height = p.Height, p.Width
		}
		return p, nil
	}
	var w, h float64
	if n, err := fmt.Sscanf(s, "%gx%g", &w, &h); n == 2 && err == nil {
		if w <= 0 || h <= 0 {
			return pdfkit.PageSize{}, fmt.Errorf("a page of %gx%g points is not a page", w, h)
		}
		if landscape {
			w, h = h, w
		}
		return pdfkit.NewPageSize(w, h), nil
	}
	return pdfkit.PageSize{}, fmt.Errorf("unknown page size %q: a name (%s), any of them with "+
		"-landscape, or WIDTHxHEIGHT in points", s, strings.Join(PageSizes(), ", "))
}

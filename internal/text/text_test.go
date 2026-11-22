// ##############################################################################
// Script Name: internal/text/text_test.go
// Description: Tests for text.
// Usage: go test ./...
// Author: Stefan Pejcic
// Created: 21.11.2026
// Last Modified: 20.11.2026
// Company: openpanel.com
// Copyright (c) openpanel.com
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
// ##############################################################################

package text

import "testing"

func TestXargs(t *testing.T) {
	cases := map[string]string{`  "1.2.3" `: "1.2.3", "a  b\n c": "a b c", `'x y'`: "x y", "": ""}
	for in, want := range cases {
		if got := Xargs(in); got != want {
			t.Errorf("Xargs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSedRepl(t *testing.T) {
	g := []string{"max_cpu=95", "max_cpu="}
	if got := SedRepl(`\1test&x`, g); got != "max_cpu=testmax_cpu=95x" {
		t.Errorf("got %q", got)
	}
	if got := SedRepl(`\1a\\b`, g); got != `max_cpu=a\b` {
		t.Errorf("got %q", got)
	}
}

func TestVersionLess(t *testing.T) {
	if !VersionLess("1.2.9", "1.2.10") || VersionLess("2.0.0", "1.9.9") || !VersionLess("1.2", "1.2.1") {
		t.Error("sort -V order is wrong")
	}
}

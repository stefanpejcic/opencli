// ##############################################################################
// Script Name: internal/passwd/passwd_test.go
// Description: Tests for passwd.
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

package passwd

import "testing"

func TestScore(t *testing.T) {
	// expected values come from lib/password_strength.sh run under C.UTF-8
	cases := map[string]int{"": 0, "abc": 17, "abcdefgh": 33, "Abcdefgh1!": 83, "Abcdefgh1234!": 100, "šđčćž": 0, "ŠĐ": 0, "abc€": 33, "€€€€€€€€": 33}
	for in, want := range cases {
		if got := Score(in); got != want {
			t.Errorf("Score(%q) = %d, want %d", in, got, want)
		}
	}
}

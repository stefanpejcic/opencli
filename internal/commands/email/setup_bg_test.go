// ##############################################################################
// Script Name: internal/commands/email/setup_bg_test.go
// Description: Tests for email-setup-bg.
// Usage: go test ./...
// Author: Stefan Pejcic
// Created: 22.11.2026
// Last Modified: 22.11.2026
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

package email

import "testing"

func TestSafeMailPathPart(t *testing.T) {
	for _, ok := range []string{"john", "john_doe", "john.doe+tag", "info-2", "example.com", "a%b"} {
		if !safeMailPathPart(ok) {
			t.Errorf("%q should be allowed", ok)
		}
		if !suEmailRe.MatchString(ok + "@example.com") {
			t.Errorf("%q@example.com should pass the address check too", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "a..b", ".hidden", "-rf", "a/b", "a b", "a;b", "$(id)"} {
		if safeMailPathPart(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

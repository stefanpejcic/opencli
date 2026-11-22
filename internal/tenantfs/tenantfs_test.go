// ##############################################################################
// Script Name: internal/tenantfs/tenantfs_test.go
// Description: Tests for tenantfs.
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

package tenantfs

import (
	"io/fs"
	"testing"
)

func TestApplyMode(t *testing.T) {
	m, _ := ApplyMode(0o644, "g+w")
	if m != 0o664 {
		t.Errorf("g+w: %o", m)
	}
	m, _ = ApplyMode(fs.ModeDir|0o755, "2775")
	if m.Perm() != 0o775 || m&fs.ModeSetgid == 0 {
		t.Errorf("2775: %v", m)
	}
}

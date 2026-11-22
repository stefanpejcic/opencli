// ##############################################################################
// Script Name: internal/text/text.go
// Description: Small text helpers that copy what common shell idioms (xargs, sort -V, awk fields) did.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 20.11.2026
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

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Xargs mimics `| xargs` with no command: quotes are removed, words are joined by single spaces
func Xargs(s string) string {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case unicode.IsSpace(r):
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return strings.Join(words, " ")
}

// Field is awk '{print $n}' for one line, 1 based, "" when missing
func Field(line string, n int) string {
	f := strings.Fields(line)
	if n < 1 || n > len(f) {
		return ""
	}
	return f[n-1]
}

// FirstLine is `| head -n1`
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Lines splits output into lines, nil for ""
func Lines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// NoSpace is `tr -d '[:space:]'`
func NoSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

var verPart = regexp.MustCompile(`[0-9]+|[^0-9]+`)

// VersionLess orders like sort -V for plain dotted versions and tags
func VersionLess(a, b string) bool {
	pa, pb := verPart.FindAllString(a, -1), verPart.FindAllString(b, -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, y := pa[i], pb[i]
		xn, xe := strconv.ParseUint(x, 10, 64)
		yn, ye := strconv.ParseUint(y, 10, 64)
		if xe == nil && ye == nil {
			if xn != yn {
				return xn < yn
			}
			continue
		}
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// Atoi is a lenient strconv.Atoi that returns 0 on junk, like bash arithmetic on an empty var
func Atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// IsInt reports ^[0-9]+$
func IsInt(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SedRepl expands a sed s/// replacement: & is the match, \N a group, \x a literal x
func SedRepl(repl string, groups []string) string {
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		switch {
		case c == '&':
			b.WriteString(groups[0])
		case c == '\\' && i+1 < len(repl):
			i++
			n := repl[i]
			if n >= '0' && n <= '9' && int(n-'0') < len(groups) {
				b.WriteString(groups[n-'0'])
			} else if n == 'n' {
				b.WriteByte('\n')
			} else if n == 't' {
				b.WriteByte('\t')
			} else {
				b.WriteByte(n)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

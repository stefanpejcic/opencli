// ##############################################################################
// Script Name: internal/commands/userreport/util.go
// Description: Small helpers shared by the user report commands.
// Usage: imported by the userreport commands
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

package userreport

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// isTTY is [[ -t fd ]]
func isTTY(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// echoE expands backslash escapes like bash's echo -e, ok is false after \c
func echoE(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'c':
			return b.String(), false
		case 'e', 'E':
			b.WriteByte(27)
		case 'f':
			b.WriteByte(12)
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte(11)
		case '\\':
			b.WriteByte('\\')
		case '0':
			n, j := 0, i+1
			for ; j < len(s) && j < i+4 && s[j] >= '0' && s[j] <= '7'; j++ {
				n = n*8 + int(s[j]-'0')
			}
			b.WriteByte(byte(n))
			i = j - 1
		case 'x':
			j := i + 1
			for ; j < len(s) && j < i+3 && isHex(s[j]); j++ {
			}
			if j == i+1 {
				b.WriteString(`\x`)
				continue
			}
			n, _ := strconv.ParseUint(s[i+1:j], 16, 8)
			b.WriteByte(byte(n))
			i = j - 1
		case 'u', 'U':
			max := 4
			if s[i] == 'U' {
				max = 8
			}
			j := i + 1
			for ; j < len(s) && j < i+1+max && isHex(s[j]); j++ {
			}
			if j == i+1 {
				b.WriteByte('\\')
				b.WriteByte(s[i])
				continue
			}
			n, _ := strconv.ParseUint(s[i+1:j], 16, 32)
			b.WriteRune(rune(n))
			i = j - 1
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// echoe prints like echo -e
func echoe(s string) {
	out, nl := echoE(s)
	fmt.Print(out)
	if nl {
		fmt.Println()
	}
}

func ifsWS(c byte, ifs string) bool {
	return strings.IndexByte(ifs, c) >= 0 && (c == ' ' || c == '\t' || c == '\n')
}

// breadTrim drops leading and trailing ifs whitespace
func breadTrim(s, ifs string) string {
	for s != "" && ifsWS(s[0], ifs) {
		s = s[1:]
	}
	for s != "" && ifsWS(s[len(s)-1], ifs) {
		s = s[:len(s)-1]
	}
	return s
}

// breadWord takes one field off s the way read does, the rest starts at the next field
func breadWord(s, ifs string) (string, string) {
	skip := func(s string) string {
		for s != "" && ifsWS(s[0], ifs) {
			s = s[1:]
		}
		return s
	}
	s = skip(s)
	end := 0
	for end < len(s) && strings.IndexByte(ifs, s[end]) < 0 {
		end++
	}
	w := s[:end]
	s = skip(s[end:])
	if s != "" && strings.IndexByte(ifs, s[0]) >= 0 && !ifsWS(s[0], ifs) {
		s = skip(s[1:])
	}
	return w, s
}

// bread splits a line like `IFS=<ifs> read -r a b c` does, empty runs of ifs whitespace collapse
func bread(line, ifs string, n int) []string {
	out := make([]string, n)
	s := line
	for s != "" && ifsWS(s[0], ifs) {
		s = s[1:]
	}
	for i := 0; i < n-1; i++ {
		if s == "" {
			return out
		}
		out[i], s = breadWord(s, ifs)
	}
	for s != "" && ifsWS(s[len(s)-1], ifs) {
		s = s[:len(s)-1]
	}
	if n > 0 && s != "" {
		if w, rest := breadWord(s, ifs); rest == "" {
			out[n-1] = w
		} else {
			out[n-1] = s
		}
	}
	return out
}

// readLines is what a `while read` loop over a file sees, a last line without newline is skipped
func readLines(content string) []string {
	if content == "" {
		return nil
	}
	parts := strings.Split(content, "\n")
	return parts[:len(parts)-1]
}

// hereLines is what `while read` sees from <<< "$var", always at least one (maybe empty) line
func hereLines(s string) []string {
	return strings.Split(s, "\n")
}

// countLines is grep -c . on a string
func countLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			n++
		}
	}
	return n
}

// nonEmpty is grep . on a string
func nonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// bcDiv prints n/d with scale=2 the way bc does, truncated and without a leading zero
func bcDiv(hundredths int64) string {
	if hundredths == 0 {
		return "0"
	}
	neg := hundredths < 0
	if neg {
		hundredths = -hundredths
	}
	s := fmt.Sprintf("%d.%02d", hundredths/100, hundredths%100)
	if hundredths < 100 {
		s = s[1:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// jqValid is `jq -e . >/dev/null <<< "$s"`: every value parses and the last one isn't null or false
func jqValid(s string) (*ojson.Object, bool) {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var last any
	n := 0
	for {
		var raw json.RawMessage
		if err := d.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			return nil, false
		}
		v, err := ojson.Parse(string(raw))
		if err != nil {
			return nil, false
		}
		last = v
		n++
	}
	if n == 0 || last == nil || last == false {
		return nil, false
	}
	o, _ := last.(*ojson.Object)
	if o == nil {
		o = ojson.New()
	}
	return o, true
}

// jqStr is jq -r '.key' on an object, "null" when missing
func jqStr(o *ojson.Object, key string) string {
	v, _ := o.Get(key)
	return ojson.Text(v)
}

// sedPrefix is `sed 's/^/  /'` on text that ends with a newline
func sedPrefix(s, prefix string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		b.WriteString(prefix + l + "\n")
	}
	return b.String()
}

// enabledModule is grep -E '^enabled_modules=' file | grep -qE '[",]name[",]'
func enabledModule(name string) bool {
	re := regexp.MustCompile(`[",]` + name + `[",]`)
	for _, l := range conf.Lines(conf.Panel) {
		if strings.HasPrefix(l, "enabled_modules=") && re.MatchString(l) {
			return true
		}
	}
	return false
}

// hasLine is grep -qx line file
func hasLine(file, line string) bool {
	for _, l := range strings.Split(conf.Read(file), "\n") {
		if l == line {
			return true
		}
	}
	return false
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// activeUsers is `opencli user-list --local --json | grep -v SUSPENDED | awk -F'"' '/username/ {print $4}'`
func activeUsers() []string {
	out, _ := sh.Opencli("user-list", "--local", "--json").Out()
	var users []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "SUSPENDED") || !strings.Contains(l, "username") {
			continue
		}
		users = append(users, awkPart(l, `"`, 4))
	}
	return strings.Fields(strings.Join(users, "\n"))
}

// trimNL drops trailing newlines like $(...) does
func trimNL(s string) string { return strings.TrimRight(s, "\n") }

func unsafePtr[T any](p *T) unsafe.Pointer { return unsafe.Pointer(p) }

func insecureTLS() *tls.Config { return &tls.Config{InsecureSkipVerify: true} }

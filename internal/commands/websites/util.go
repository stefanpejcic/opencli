// ##############################################################################
// Script Name: internal/commands/websites/util.go
// Description: Shared helpers for the websites commands
// Usage: imported by command packages
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

package websites

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"opencli/internal/sh"
)

const (
	colGreen  = "\033[0;32m"
	colYellow = "\033[0;33m"
	colReset  = "\033[0m"
)

func umask() os.FileMode {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return os.FileMode(m)
}

// readFields is `IFS=<ifs> read -r a b c ...` for one line, the last name gets the rest
func readFields(line string, n int, ifs string) []string {
	out := make([]string, n)
	rest := strings.TrimLeft(line, ifs)
	for i := 0; i < n-1; i++ {
		j := strings.IndexAny(rest, ifs)
		if j < 0 {
			out[i], rest = rest, ""
			break
		}
		out[i] = rest[:j]
		rest = strings.TrimLeft(rest[j:], ifs)
	}
	if rest != "" || n == 1 {
		out[n-1] = strings.TrimRight(rest, ifs)
	}
	return out
}

// sedHTTP is sed -E 's~https?://~~' on every line
func sedHTTP(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		a, b := strings.Index(l, "https://"), strings.Index(l, "http://")
		switch {
		case a >= 0 && (b < 0 || a < b):
			l = l[:a] + l[a+8:]
		case b >= 0:
			l = l[:b] + l[b+7:]
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// reportName is `sed 's|https\?://||' | sed 's|/|_|g'`
func reportName(website string) string {
	return strings.ReplaceAll(sedHTTP(website), "/", "_")
}

// userNames is `opencli user-list --local --json | grep -v SUSPENDED | awk -F'"' '/username/ {print $4}'`
func userNames() string {
	out, _ := sh.Opencli("user-list", "--local", "--json").Out()
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "SUSPENDED") || !strings.Contains(line, "username") {
			continue
		}
		if f := strings.Split(line, `"`); len(f) >= 4 {
			names = append(names, f[3])
		} else {
			names = append(names, "")
		}
	}
	return strings.TrimRight(strings.Join(names, "\n"), "\n")
}

func now() string { return time.Now().Format("2006-01-02 15:04:05") }

var outMu sync.Mutex

// appendFile is `>> file`, serialized so parallel jobs don't mix lines
func appendFile(path, s string) {
	outMu.Lock()
	defer outMu.Unlock()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: cannot write\n", path)
		return
	}
	f.WriteString(s)
	f.Close()
}

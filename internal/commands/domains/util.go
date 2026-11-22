// ##############################################################################
// Script Name: internal/commands/domains/util.go
// Description: Check which username owns a certain domain name.
// Usage: opencli domains-whoowns <DOMAIN-NAME> [--context] [--docroot]
// Author: Stefan Pejcic
// Created: 01.10.2023
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

package domains

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"opencli/internal/db"
)

// readFields is bash `read -r a b c` with default IFS, the last name gets the rest of the line
func readFields(s string, n int) []string {
	return splitIFS(s, n, " \t\n")
}

// readTabs is bash `IFS=$'\t' read -r a b c`, tabs collapse like any IFS whitespace
func readTabs(s string, n int) []string {
	return splitIFS(s, n, "\t")
}

func splitIFS(s string, n int, ifs string) []string {
	res := make([]string, n)
	s = strings.TrimLeft(s, ifs)
	for i := 0; i < n; i++ {
		if i == n-1 {
			res[i] = strings.TrimRight(s, ifs)
			break
		}
		j := strings.IndexAny(s, ifs)
		if j < 0 {
			res[i] = s
			break
		}
		res[i] = s[:j]
		s = strings.TrimLeft(s[j:], ifs)
	}
	return res
}

// dbOut is `mariadb --defaults-extra-file -D panel -e q -sN` with an optional timeout
func dbOut(q string, timeout time.Duration) string {
	c := db.Cmd(q, "-s", "-N")
	if timeout > 0 {
		c = c.Timeout(timeout)
	}
	out, _ := c.Out()
	return out
}

// awkSecondField is awk -F sep '{print $2}' over all lines, sep is a regex, trailing newlines trimmed
func awkSecondField(out, sep string) string {
	re, err := regexp.Compile(sep)
	if err != nil {
		return ""
	}
	var res []string
	for _, l := range strings.Split(out, "\n") {
		parts := re.Split(l, -1)
		if len(parts) > 1 {
			res = append(res, parts[1])
		} else {
			res = append(res, "")
		}
	}
	return strings.TrimRight(strings.Join(res, "\n"), "\n")
}

// curlGet is `curl --silent --max-time N -4 url`, any http answer counts, ok is false on transport errors
func curlGet(url string, timeout time.Duration) (string, bool) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		},
	}
	c := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err == nil
}

// isTTY is what bash checks before read -p prints its prompt
func isTTY(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// readLineTimeout is bash `read -t N`, empty on timeout or EOF
func readLineTimeout(d time.Duration) string {
	ch := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		ch <- strings.TrimSuffix(l, "\n")
	}()
	select {
	case s := <-ch:
		return s
	case <-time.After(d):
		return ""
	}
}

// chownLike gives dst the owner of the stat'ed source, like cp -a does
func chownLike(dst string, st os.FileInfo) {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		os.Lchown(dst, int(s.Uid), int(s.Gid))
	}
}

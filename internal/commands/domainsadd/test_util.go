// ##############################################################################
// Script Name: internal/commands/domainsadd/test_util.go
// Description: Helpers for domains-test.
// Usage: opencli domains-test <DOMAIN_NAME>[/SUBFOLDER] [RUNS] [--load <N|auto>]
// Author: Stefan Pejcic
// Created: 28.09.2026
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

package domainsadd

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// curlOpts is the part of curl's command line the speed test uses
type curlOpts struct {
	url     string
	dial    map[string]string
	headers [][2]string
	http1   bool
	timeout time.Duration
	ua      string
}

type curlRes struct {
	code     string
	start    float64
	pre      float64
	body     string
	respHdr  http.Header
	hasReply bool
}

// curlTimed is one `curl -sk` run: time_starttransfer, time_pretransfer and http_code, "000" when nothing answered
func curlTimed(o curlOpts, wantBody bool) curlRes {
	tr := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true, CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if t, ok := o.dial[addr]; ok {
				addr = t
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
		},
	}
	if o.http1 {
		tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	} else {
		tr.ForceAttemptHTTP2 = true
	}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	res := curlRes{code: "000"}
	t0 := time.Now()
	trace := &httptrace.ClientTrace{
		GotConn:              func(httptrace.GotConnInfo) { res.pre = time.Since(t0).Seconds() },
		GotFirstResponseByte: func() { res.start = time.Since(t0).Seconds() },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", o.url, nil)
	if err != nil {
		return res
	}
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	for _, h := range o.headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		res.start, res.pre = 0, 0
		return res
	}
	defer resp.Body.Close()
	res.hasReply = true
	res.code = strconv.Itoa(resp.StatusCode)
	res.respHdr = resp.Header
	if wantBody {
		b, _ := io.ReadAll(resp.Body)
		res.body = string(b)
	} else {
		io.Copy(io.Discard, resp.Body)
	}
	return res
}

func stdoutIsTerminal() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// awkNum is how awk reads a string as a number, the leading numeric part or 0
func awkNum(s string) float64 {
	m := regexp.MustCompile(`^\s*[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?`).FindString(s)
	if m == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(m), 64)
	return f
}

func gt(a, b string) bool { return awkNum(a) > awkNum(b) }

func subMs(a, b string) string { return fmt.Sprintf("%.0f", awkNum(a)-awkNum(b)) }

func padR(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func padL(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

// envName is `tr 'a-z.-' 'A-Z__'`
func envName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 32
		case r == '.' || r == '-':
			return '_'
		}
		return r
	}, s)
}

// median is the bash helper: drops the warm-up line, sorts, takes the middle, the code is the one of the slowest run
func median(lines []string) (string, string) {
	if len(lines) > 0 {
		lines = lines[1:]
	}
	type row struct {
		t    float64
		code string
		line string
	}
	var rows []row
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.Fields(l)
		c := ""
		if len(f) > 1 {
			c = f[1]
		}
		rows = append(rows, row{awkNum(l), c, l})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].t != rows[j].t {
			return rows[i].t < rows[j].t
		}
		return rows[i].line < rows[j].line
	})
	n := len(rows)
	if n == 0 {
		return "-", "000"
	}
	ts := make([]float64, n)
	for i, r := range rows {
		ts[i] = awkNum(strings.Fields(r.line)[0]) * 1000
	}
	var m float64
	if n%2 == 1 {
		m = ts[(n+1)/2-1]
	} else {
		m = (ts[n/2-1] + ts[n/2]) / 2
	}
	return fmt.Sprintf("%.1f", m), rows[n-1].code
}

// printfB is the %b of bash printf, backslash escapes in the argument
func printfB(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'c':
			return b.String()
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
		case '0':
			j, v := i+1, 0
			for k := 0; k < 3 && j < len(s) && s[j] >= '0' && s[j] <= '7'; k++ {
				v = v*8 + int(s[j]-'0')
				j++
			}
			b.WriteByte(byte(v))
			i = j - 1
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// grepCount is `... | grep -c pat`
func grepCount(s, pat string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, pat) {
			n++
		}
	}
	return n
}

func isNum(s string) bool { return regexp.MustCompile(`^[0-9]+$`).MatchString(s) }

func nproc() int { return runtime.NumCPU() }

// ##############################################################################
// Script Name: internal/commands/user/util.go
// Description: Shared helpers for the user commands
// Usage: -
// Author: Stefan Pejcic
// Created: 16.11.2023
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

package user

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"opencli/internal/db"
	"opencli/internal/sh"
)

const (
	colGreen = "\033[0;32m"
	colRed   = "\033[0;31m"
	colReset = "\033[0m"
)

// sentinelBg is `nohup opencli sentinel ... &`
func sentinelBg(args ...string) {
	sh.Opencli(append([]string{"sentinel"}, args...)...).Quiet().Background()
}

// lines counts like wc -l on output that was already trimmed
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func eprintln(s string) { fmt.Fprintln(os.Stderr, s) }

// readPrompt is `read -r -p`, the prompt only shows on a terminal
func readPrompt(prompt string) string {
	if isTTY(os.Stdin) {
		fmt.Fprint(os.Stderr, prompt)
	}
	s, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSuffix(s, "\n")
}

// copyFile is plain cp, keeps the source mode
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, st.Mode().Perm())
}

// reloadCaddyBg is the `nohup podman exec caddy sh -c "caddy validate && caddy reload" &` bit
func reloadCaddyBg() {
	sh.C("podman", "exec", "caddy", "sh", "-c", "caddy validate && caddy reload").Quiet().Background()
}

// forEachPar runs fn for every item with at most n at a time, like xargs -P
func forEachPar(items []string, n int, fn func(string)) {
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for _, it := range items {
		sem <- struct{}{}
		wg.Add(1)
		go func(it string) {
			defer func() { <-sem; wg.Done() }()
			fn(it)
		}(it)
	}
	wg.Wait()
}

// curl4 is `curl --silent --max-time N -4 url`, ok is false when curl would have failed
func curl4(url string, timeout time.Duration) (string, bool) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		},
		TLSClientConfig: &tls.Config{},
	}
	c := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return strings.TrimRight(string(b), "\n"), err == nil
}

// userIDCtx is the `mariadb -se` + awk lookup of id and server
func userIDCtx(username string) (string, string) {
	info, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+db.Escape(username)+"';", "-s").Out()
	f := strings.Fields(strings.SplitN(info, "\n", 2)[0])
	id, ctx := "", ""
	if len(f) > 0 {
		id = f[0]
	}
	if len(f) > 1 {
		ctx = f[1]
	}
	return id, ctx
}

// isTTY is [ -t fd ]
func isTTY(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// ##############################################################################
// Script Name: internal/commands/update/update_net.go
// Description: Check if update is available, install updates.
// Usage: opencli update [--check | --force [--no-restart] | --admin | --panel | --cli | --translations | --system | --modules | --compose | --env | --php | --wp | --ols | --litespeed | --apache | --clamav | --phpmyadmin | --postgres | --skeleton | --ssh | --varnish | --cron | --caddy]
// Author: Stefan Pejcic
// Created: 10.10.2023
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

package update

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// http helpers that stand in for the curl and wget calls of update.sh

func httpClient(connect, total time.Duration, follow bool) *http.Client {
	d := &net.Dialer{Timeout: connect}
	c := &http.Client{Timeout: total, Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   connect,
		ResponseHeaderTimeout: 0,
	}}
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return c
}

// curlBody is curl -s url, no timeout and no redirects like the script
func curlBody(url string, total time.Duration) string {
	resp, err := httpClient(0, total, false).Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// curlDownload is curl -sS --connect-timeout --max-time --retry N -o dest -w "%{http_code}"
func curlDownload(url, dest string, connect, total time.Duration, retries int) string {
	wait := time.Second
	for try := 0; ; try++ {
		code, err := curlOnce(url, dest, connect, total)
		transient := isTimeout(err) || code == "408" || code == "429" || code == "500" || code == "502" || code == "503" || code == "504"
		if !transient || try >= retries {
			return code
		}
		time.Sleep(wait)
		wait *= 2
	}
}

func curlOnce(url, dest string, connect, total time.Duration) (string, error) {
	resp, err := httpClient(connect, total, false).Get(url)
	if err != nil {
		return "000", err
	}
	defer resp.Body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return strconv.Itoa(resp.StatusCode), err
	}
	_, err = io.Copy(f, resp.Body)
	f.Close()
	return strconv.Itoa(resp.StatusCode), err
}

// wgetDownload is wget --timeout=T --tries=N -q -O dest url, returns wget's exit code
func wgetDownload(ctx context.Context, url, dest string, timeout time.Duration, tries int) int {
	f, err := os.Create(dest)
	if err != nil {
		return 3
	}
	defer f.Close()
	code := 4
	for try := 0; try < tries; try++ {
		if try > 0 {
			f.Truncate(0)
			f.Seek(0, 0)
		}
		code = wgetOnce(ctx, url, f, timeout)
		// wget doesn't retry 4xx answers
		if code == 0 || code == 8 || ctx.Err() != nil {
			break
		}
	}
	return code
}

func wgetOnce(ctx context.Context, url string, w io.Writer, timeout time.Duration) int {
	c := httpClient(timeout, 0, true)
	c.Transport.(*http.Transport).ResponseHeaderTimeout = timeout
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 1
	}
	resp, err := c.Do(req)
	if err != nil {
		return 4
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 8
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return 4
	}
	return 0
}

// wgetSpider is wget --spider -q url
func wgetSpider(url string) bool {
	c := httpClient(0, 0, true)
	resp, err := c.Head(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

// curlHeadCode is curl -sSLI -o /dev/null -w "%{http_code}"
func curlHeadCode(url string) string {
	resp, err := httpClient(0, 0, true).Head(url)
	if err != nil {
		return "000"
	}
	resp.Body.Close()
	return strconv.Itoa(resp.StatusCode)
}

// curlSave is curl -sSL url -o dest, any http answer counts as success like curl without -f
func curlSave(url, dest string) bool {
	resp, err := httpClient(0, 0, true).Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return false
	}
	_, err = io.Copy(f, resp.Body)
	f.Close()
	return err == nil
}

// jsonStream is jq empty, every value in the file has to parse
func jsonStream(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	d := json.NewDecoder(f)
	for {
		var v any
		if err := d.Decode(&v); err == io.EOF {
			return true
		} else if err != nil {
			return false
		}
	}
}

// copyFile is cp -f, an existing target keeps its mode, preserve is cp -p
func copyFile(src, dst string, preserve bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, st.Mode().Perm())
	if err != nil {
		os.Remove(dst)
		if out, err = os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, st.Mode().Perm()); err != nil {
			return err
		}
	}
	_, err = io.Copy(out, in)
	out.Close()
	if preserve {
		os.Chmod(dst, st.Mode().Perm())
		if s, ok := st.Sys().(*syscall.Stat_t); ok {
			os.Lchown(dst, int(s.Uid), int(s.Gid))
		}
		os.Chtimes(dst, st.ModTime(), st.ModTime())
	}
	return err
}

// copyTree is cp -rf src/. dst/, symlinks are copied as links
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			st, _ := d.Info()
			if !isDir(target) {
				os.MkdirAll(target, st.Mode().Perm())
			}
		case d.Type()&fs.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			os.Remove(target)
			os.Symlink(l, target)
		default:
			copyFile(p, target, false)
		}
		return nil
	})
}

func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func isFile(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }

// firstSubdir is find dir -mindepth 1 -maxdepth 1 -type d | head -n1
func firstSubdir(dir string) string {
	f, err := os.Open(dir)
	if err != nil {
		return ""
	}
	defer f.Close()
	for {
		ents, err := f.ReadDir(64)
		for _, e := range ents {
			if e.IsDir() {
				return filepath.Join(dir, e.Name())
			}
		}
		if err != nil {
			return ""
		}
	}
}

// awkLines splits like awk reads records, the last line doesn't need a newline
func awkLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

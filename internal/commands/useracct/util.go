// ##############################################################################
// Script Name: internal/commands/useracct/util.go
// Description: Helpers shared by the user account lifecycle commands.
// Usage: imported by the useracct commands
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

package useracct

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"opencli/internal/sh"
	"opencli/internal/text"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/ojson"
)

// ids returns uid and gid for a "name:name" chown spec
func ids(name string) (int, int, bool) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, false
	}
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, 0, false
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(g.Gid)
	return uid, gid, true
}

// sysUID is `id -u name`, "" when there's no such user
func sysUID(name string) string {
	u, err := user.Lookup(name)
	if err != nil {
		return ""
	}
	return u.Uid
}

// sysGID is `id -g name`
func sysGID(name string) string {
	u, err := user.Lookup(name)
	if err != nil {
		return ""
	}
	return u.Gid
}

// chownName is chown -R name:name, links get their own owner like chown -R does
func chownName(path, name string) {
	if uid, gid, ok := ids(name); ok {
		lchownR(path, uid, gid)
	} else {
		fmt.Fprintf(os.Stderr, "chown: invalid user: '%s:%s'\n", name, name)
	}
}

func lchownR(path string, uid, gid int) {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		os.Lchown(p, uid, gid)
		return nil
	})
}

// chmodR is chmod -R with an octal mode, symlinks inside are skipped like chmod's walk
func chmodR(path string, mode fs.FileMode) {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != path && d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		os.Chmod(p, mode)
		return nil
	})
}

// addMode is chmod -R with a "+bits" mode like g+w or +rx
func addModeR(path string, bits fs.FileMode) {
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != path && d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if st, err := os.Stat(p); err == nil {
			os.Chmod(p, st.Mode().Perm()|st.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)|bits)
		}
		return nil
	})
}

// addMode is a non recursive chmod +bits
func addMode(path string, bits fs.FileMode) {
	if st, err := os.Stat(path); err == nil {
		os.Chmod(path, st.Mode().Perm()|st.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)|bits)
	}
}

func umask() fs.FileMode {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return fs.FileMode(m)
}

// cpFile is plain cp: an existing target keeps its mode, a new one gets the source mode minus umask
func cpFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if dt, err := os.Stat(dst); err == nil && dt.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cp prints cp's own error for a missing source
func cp(src, dst string) bool {
	if _, err := os.Stat(src); err != nil {
		fmt.Fprintf(os.Stderr, "cp: cannot stat '%s': No such file or directory\n", src)
		return false
	}
	return cpFile(src, dst) == nil
}

// cpR is cp -r: into dst when it's a dir, as dst otherwise, links are copied as links
func cpR(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if dt, err := os.Stat(dst); err == nil && dt.IsDir() {
		dst = filepath.Join(dst, filepath.Base(strings.TrimRight(src, "/")))
	}
	return copyTree(src, dst, st, false)
}

// copyTree copies src to dst, keep is cp -a (owners, modes and times)
func copyTree(src, dst string, st fs.FileInfo, keep bool) error {
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		os.Remove(dst)
		if err := os.Symlink(t, dst); err != nil {
			return err
		}
	case st.IsDir():
		if err := os.Mkdir(dst, st.Mode().Perm()); err != nil && !os.IsExist(err) {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		var first error
		for _, e := range ents {
			es, err := os.Lstat(filepath.Join(src, e.Name()))
			if err == nil {
				err = copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), es, keep)
			}
			if err != nil && first == nil {
				first = err
			}
		}
		if keep {
			os.Chmod(dst, st.Mode()&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky))
		}
		if first != nil {
			return first
		}
	case st.Mode().IsRegular():
		if err := cpFile(src, dst); err != nil {
			return err
		}
		if keep {
			os.Chmod(dst, st.Mode()&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky))
		}
	default:
		return nil
	}
	if keep {
		if s, ok := st.Sys().(*syscall.Stat_t); ok {
			os.Lchown(dst, int(s.Uid), int(s.Gid))
			if st.Mode()&fs.ModeSymlink == 0 {
				os.Chtimes(dst, time.Unix(s.Atim.Sec, s.Atim.Nsec), st.ModTime())
			}
		}
	}
	return nil
}

// cpA is cp -a src dst (into dst when it's a dir)
func cpA(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if dt, err := os.Stat(dst); err == nil && dt.IsDir() {
		dst = filepath.Join(dst, filepath.Base(strings.TrimRight(src, "/")))
	}
	return copyTree(src, dst, st, true)
}

// cpAContents is cp -a src/. dst/
func cpAContents(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	return copyTree(src, dst, st, true)
}

// sedFile applies fn to every line and writes the file back like sed -i, the last newline is kept as it was
func sedFile(path string, fn func(string) string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(b)
	nl := strings.HasSuffix(s, "\n")
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if s == "" {
		lines = nil
	}
	for i, l := range lines {
		lines[i] = fn(l)
	}
	out := strings.Join(lines, "\n")
	if nl {
		out += "\n"
	}
	return conf.WriteFileKeep(path, out, false)
}

// subRe is sed s|re|repl|g on a line, repl uses sed's & and \N
func subRe(re *regexp.Regexp, repl string, global bool) func(string) string {
	return func(l string) string {
		n := 0
		return re.ReplaceAllStringFunc(l, func(m string) string {
			n++
			if !global && n > 1 {
				return m
			}
			return sedExpand(re, m, repl)
		})
	}
}

func sedExpand(re *regexp.Regexp, m, repl string) string {
	g := re.FindStringSubmatch(m)
	if g == nil {
		g = []string{m}
	}
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		switch {
		case c == '&':
			b.WriteString(g[0])
		case c == '\\' && i+1 < len(repl):
			i++
			n := repl[i]
			switch {
			case n >= '0' && n <= '9':
				if int(n-'0') < len(g) {
					b.WriteString(g[n-'0'])
				}
			case n == 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte(n)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// jqSetNumber is jq '.key = n' file > tmp && mv tmp file
func jqSetNumber(file, key, n string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jq: error: Could not open %s: No such file or directory\n", file)
		return err
	}
	v, err := ojson.Parse(string(b))
	if err != nil {
		fmt.Fprintln(os.Stderr, "jq: error (at "+file+"): cannot parse")
		return err
	}
	num, err := ojson.Parse(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jq: invalid JSON text passed to --argjson\n")
		return err
	}
	o, ok := v.(*ojson.Object)
	if !ok {
		fmt.Fprintln(os.Stderr, "jq: error (at "+file+"): cannot index with \""+key+"\"")
		return fmt.Errorf("not an object")
	}
	o.Set(key, num)
	tmp := file + ".opencli-tmp"
	if err := os.WriteFile(tmp, []byte(ojson.Pretty(o)+"\n"), 0o666); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// jqRaw is jq -r '.key // fallback' for a top level key
func jqRaw(file, key, fallback string) (string, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	o, err := ojson.ParseObject(string(b))
	if err != nil {
		return "", false
	}
	v, ok := o.Get(key)
	if !ok || v == nil || v == false {
		return fallback, true
	}
	return ojson.Text(v), true
}

// grepLines returns the lines of path matching re, like grep
func grepLines(path string, re *regexp.Regexp) []string {
	var out []string
	for _, l := range conf.Lines(path) {
		if re.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

// hasLinePrefix is grep -q "^prefix" file
func hasLinePrefix(path, prefix string) bool {
	for _, l := range conf.Lines(path) {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// passwdField returns field n (0 based) of name's /etc/passwd line, like awk -F: '$1==u{print $n}'
func passwdField(name string, n int) string {
	var out []string
	for _, l := range conf.Lines("/etc/passwd") {
		f := strings.Split(l, ":")
		if f[0] == name && n < len(f) {
			out = append(out, f[n])
		}
	}
	return strings.Join(out, "\n")
}

func statUID(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.Itoa(int(st.Sys().(*syscall.Stat_t).Uid))
}

func statGID(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.Itoa(int(st.Sys().(*syscall.Stat_t).Gid))
}

func isSocket(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode()&fs.ModeSocket != 0
}

// nonEmptyFile is [[ -s ]]
func nonEmptyFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

// dirHasEntries is [[ -n "$(ls -A dir)" ]]
func dirHasEntries(path string) bool {
	ents, err := os.ReadDir(path)
	return err == nil && len(ents) > 0
}

// get4 is curl --silent --max-time N -4 url, ok when a response came back whatever the status
func get4(url string, timeout time.Duration) (string, bool) {
	dialer := &net.Dialer{}
	client := &http.Client{Timeout: timeout, Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", addr)
		},
	}}
	resp, err := client.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(b), "\n"), true
}

// ipAddrGlobal is ip addr | grep 'inet ' | grep global | head -n1 | awk '{print $2}' | cut -f1 -d/
func ipAddrGlobal() string {
	for _, l := range strings.Split(sh.Str("ip", "addr"), "\n") {
		if strings.Contains(l, "inet ") && strings.Contains(l, "global") {
			ip, _, _ := strings.Cut(text.Field(l, 2), "/")
			return ip
		}
	}
	return ""
}

// serverIP is the curl ip.openpanel.com || curl ifconfig.me || ip addr chain
func serverIP() string {
	if ip, ok := get4("https://ip.openpanel.com", time.Second); ok {
		return ip
	}
	if ip, ok := get4("https://ifconfig.me", time.Second); ok {
		return ip
	}
	return ipAddrGlobal()
}

// freeKB is the Available column of df -k
func freeKB(path string) (int64, bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize) / 1024, true
}

// duField is the first column of a du run, like du ... | cut -f1
func duField(args ...string) string {
	out := sh.C("du", args...).NoErr().Str()
	f, _, _ := strings.Cut(out, "\t")
	return f
}

// awkInsert formats tsv rows like the backup's awk printf, fields past the end are empty
func awkInsert(tsv, header, format string, n int) string {
	var b strings.Builder
	if header != "" {
		b.WriteString(header + "\n")
	}
	for _, l := range text.Lines(tsv) {
		f := strings.Split(l, "\t")
		args := make([]any, n)
		for i := range args {
			if i < len(f) {
				args[i] = f[i]
			} else {
				args[i] = ""
			}
		}
		b.WriteString(fmt.Sprintf(format, args...))
	}
	b.WriteString(";\n")
	return b.String()
}

// countLines is wc -l
func countLines(path string) int {
	return strings.Count(conf.Read(path), "\n")
}

// readLines is a while read loop, a last line without newline is dropped
func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	l := strings.Split(string(b), "\n")
	return l[:len(l)-1]
}

// dkimLines picks the opendkim table lines for one domain
func dkimLines(table, d string) []string {
	var out []string
	for _, l := range conf.Lines(table) {
		k := text.Field(l, 1)
		ok := k == "*@"+d || k == d || k == "*."+d
		if !ok && len(k) > len(d) {
			s := len(k) - len(d) - 12
			if s < 0 {
				s = 0
			}
			ok = k[s:] == "._domainkey."+d
		}
		if ok {
			out = append(out, l)
		}
	}
	return out
}

func appendFile(path, s string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		return
	}
	f.WriteString(s)
	f.Close()
}

func linesText(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return strings.Join(l, "\n") + "\n"
}

// postfwdFor keeps the id= lines that mention one of the domains plus the action line after each
func postfwdFor(src string, domains []string) string {
	lines := readLines(src)
	var b strings.Builder
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if !strings.HasPrefix(l, "id=") {
			continue
		}
		for _, d := range domains {
			if strings.Contains(l, "@"+d) {
				b.WriteString(l + "\n")
				if i+1 < len(lines) {
					i++
					b.WriteString(lines[i] + "\n")
				}
				break
			}
		}
	}
	return b.String()
}

// grepFile is grep -E re src > dst, dst is always created
func grepFile(src, dst string, re *regexp.Regexp) {
	os.WriteFile(dst, nil, 0o666)
	if conf.IsFile(src) {
		os.WriteFile(dst, []byte(linesText(grepLines(src, re))), 0o666)
	}
}

// mailDomainAlt is the anchored domain alternation used to pick mail config lines
func mailDomainAlt(domains []string) string {
	var p []string
	for _, d := range domains {
		p = append(p, strings.ReplaceAll(d, ".", `\.`))
	}
	return strings.Join(p, "|")
}

// pipe2 runs a | b and returns both exit codes, a's stderr goes to aErr and b's to bErr
func pipe2(a, b *exec.Cmd) (int, int) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return 1, 1
	}
	a.Stdout = pw
	b.Stdin = pr
	errA := a.Start()
	errB := b.Start()
	pw.Close()
	pr.Close()
	ra, rb := 127, 127
	if errA == nil {
		ra = sh.Code(a.Wait())
	}
	if errB == nil {
		rb = sh.Code(b.Wait())
	}
	return ra, rb
}

// sqlJoinSemicolon is sed -E ':a;N;$!ba;s/,\s*;\s*/;/g' on a whole file
func sqlJoinSemicolon(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	s := string(b)
	nl := strings.HasSuffix(s, "\n")
	s = regexp.MustCompile(`,[\s\v]*;[\s\v]*`).ReplaceAllString(strings.TrimSuffix(s, "\n"), ";")
	if nl {
		s += "\n"
	}
	conf.WriteFileKeep(path, s, false)
}

// awkSecondQuoted is awk -F"'" '/INSERT INTO plans/{getline; print $2; exit}'
func awkSecondQuoted(path string) string {
	lines := conf.Lines(path)
	for i, l := range lines {
		if strings.Contains(l, "INSERT INTO plans") {
			next := l
			if i+1 < len(lines) {
				next = lines[i+1]
			}
			f := strings.Split(next, "'")
			if len(f) > 1 {
				return f[1]
			}
			return ""
		}
	}
	return ""
}

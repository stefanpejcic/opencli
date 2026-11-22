// ##############################################################################
// Script Name: internal/conf/conf.go
// Description: Reading and writing key=value config files the way grep, cut and sed did.
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

package conf

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// Common config file paths
const (
	Panel     = "/etc/openpanel/openpanel/conf/openpanel.config"
	AdminINI  = "/etc/openpanel/openadmin/config/admin.ini"
	UsersDir  = "/etc/openpanel/openpanel/core/users"
	Caddyfile = "/etc/openpanel/caddy/Caddyfile"
	RootEnv   = "/root/.env"
)

// Lines reads a file into lines, nil when it can't be read
func Lines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Raw is `grep "^key=" file | cut -d= -f2-` keeping only the first match, ok tells if the key exists
func Raw(path, key string) (string, bool) {
	for _, l := range Lines(path) {
		if strings.HasPrefix(l, key+"=") {
			return l[len(key)+1:], true
		}
	}
	return "", false
}

// Last is the same as Raw but takes the last match, like `| tail -n1`
func Last(path, key string) (string, bool) {
	val, found := "", false
	for _, l := range Lines(path) {
		if strings.HasPrefix(l, key+"=") {
			val, found = l[len(key)+1:], true
		}
	}
	return val, found
}

// Get returns the value with surrounding quotes and spaces stripped, "" when missing
func Get(path, key string) string {
	v, _ := Raw(path, key)
	return Unquote(v)
}

// Unquote trims spaces and one pair of matching quotes
func Unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return v
}

// StripQuotes deletes every quote, like `tr -d '"' | tr -d "'"`
func StripQuotes(v string) string {
	return strings.NewReplacer(`"`, "", `'`, "").Replace(v)
}

// Env reads KEY from an .env file the way the scripts did: last match, quotes and whitespace removed
func Env(path, key string) string {
	v, _ := Last(path, key)
	v = StripQuotes(v)
	return strings.Join(strings.Fields(v), "")
}

// Set replaces the value of key in place keeping the file's inode, adds the line when missing if add is true
func Set(path, key, value string, add bool) error {
	lines := Lines(path)
	done := false
	for i, l := range lines {
		if strings.HasPrefix(l, key+"=") {
			lines[i] = key + "=" + value
			done = true
		}
	}
	if !done {
		if !add {
			return nil
		}
		lines = append(lines, key+"="+value)
	}
	return WriteInPlace(path, strings.Join(lines, "\n")+"\n")
}

// WriteInPlace overwrites a file keeping its inode, owner and mode, like `cat tmp > file`
func WriteInPlace(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Exists reports whether a path exists
func Exists(path string) bool { _, err := os.Stat(path); return err == nil }

// IsFile reports whether path is a regular file, like [[ -f ]]
func IsFile(path string) bool { st, err := os.Stat(path); return err == nil && st.Mode().IsRegular() }

// IsDir reports whether path is a directory, like [[ -d ]]
func IsDir(path string) bool { st, err := os.Stat(path); return err == nil && st.IsDir() }

// Read returns a file's content or ""
func Read(path string) string { b, _ := os.ReadFile(path); return string(b) }

// ModuleEnabled checks enabled_modules in openpanel.config for a module name
func ModuleEnabled(name string) bool {
	v, _ := Raw(Panel, "enabled_modules")
	for _, m := range strings.Split(StripQuotes(v), ",") {
		if strings.TrimSpace(m) == name {
			return true
		}
	}
	return false
}

// WriteFileKeep writes content like sed -i does: a new file renamed over the old one with the same mode and owner, skip when unchanged and lazy is set
func WriteFileKeep(path, content string, lazy bool) error {
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if lazy && string(old) == content {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := path + ".opencli-tmp"
	if err := os.WriteFile(tmp, []byte(content), st.Mode().Perm()); err != nil {
		return err
	}
	os.Chmod(tmp, st.Mode().Perm())
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		os.Chown(tmp, int(s.Uid), int(s.Gid))
	}
	return os.Rename(tmp, path)
}

// SedFile edits a file line by line like sed -i, prints sed's error and returns false when it can't be read
func SedFile(path string, edit func(lines []string) []string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sed: can't read %s: No such file or directory\n", path)
		return false
	}
	s := string(b)
	nl := strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	var lines []string
	if s != "" || nl {
		lines = strings.Split(s, "\n")
	}
	lines = edit(lines)
	out := strings.Join(lines, "\n")
	if nl && len(lines) > 0 {
		out += "\n"
	}
	WriteFileKeep(path, out, false)
	return true
}

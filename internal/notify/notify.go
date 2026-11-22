// ##############################################################################
// Script Name: internal/notify/notify.go
// Description: Reading and writing OpenAdmin notifications, one JSON object per line.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 24.09.2026
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

package notify

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"opencli/internal/conf"
	"opencli/internal/ojson"
)

// Log is the notifications file, lines that aren't json (before 2.0.12) are left untouched
const (
	Log  = "/var/log/openpanel/admin/notifications.log"
	lock = Log + ".lock"
	// unread entries older than this stop deduping, so a long-ignored alert gets reported again
	repeatAfter = 86400 * time.Second
)

const stamp = "2006-01-02 15:04:05"

func initLog() {
	os.MkdirAll(filepath.Dir(Log), 0o755)
	if _, err := os.Stat(Log); err != nil {
		os.WriteFile(Log, nil, 0o644)
	}
}

func withLock(fn func()) {
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		defer func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }()
	}
	fn()
}

type line struct {
	raw string
	obj *ojson.Object
}

func read() []line {
	var out []line
	for _, l := range conf.Lines(Log) {
		o, err := ojson.ParseObject(l)
		if err != nil {
			o = nil
		}
		out = append(out, line{raw: l, obj: o})
	}
	return out
}

// write keeps the inode and permissions like cat tmp > log
func write(lines []line) {
	var b strings.Builder
	for _, l := range lines {
		if l.obj != nil {
			b.WriteString(ojson.Compact(l.obj))
		} else {
			b.WriteString(l.raw)
		}
		b.WriteByte('\n')
	}
	conf.WriteInPlace(Log, b.String())
}

func since() string { return time.Now().Add(-repeatAfter).Format(stamp) }

func recentUnread(o *ojson.Object, title, since string) bool {
	return o.Str("status") == "unread" && o.Str("title") == title && o.Str("time") >= since
}

func recentInfo(o *ojson.Object, title, since string) bool {
	return o.Str("severity") == "info" && o.Str("title") == title && o.Str("time") >= since
}

func count(lines []line, match func(*ojson.Object) bool) int {
	n := 0
	for _, l := range lines {
		if l.obj != nil && match(l.obj) {
			n++
		}
	}
	return n
}

func bump(o *ojson.Object) {
	c := 1.0
	if v, ok := o.Get("count"); ok && v != nil {
		if n, ok := v.(json.Number); ok {
			c, _ = n.Float64()
		}
	}
	o.Set("count", json.Number(fmt.Sprint(c+1)))
}

// IsUnread reports a recent unread entry with this title
func IsUnread(title string) bool {
	initLog()
	s := since()
	return count(read(), func(o *ojson.Object) bool { return recentUnread(o, title, s) }) > 0
}

// Add writes an entry, dedup "yes" bumps a recent unread one with the same title, "info" bumps recent info ones and swaps the message
func Add(dedup, status, severity, category, source, title, message, details string) bool {
	now := time.Now().Format(stamp)
	rnd := make([]byte, 3)
	rand.Read(rnd)
	id := fmt.Sprintf("%d%s", time.Now().Unix(), hex.EncodeToString(rnd))
	var det any
	if details != "" {
		if v, err := ojson.Parse(details); err == nil {
			det = v
		}
	}
	initLog()
	added := false
	s := since()
	withLock(func() {
		lines := read()
		switch {
		case dedup == "yes" && count(lines, func(o *ojson.Object) bool { return recentUnread(o, title, s) }) > 0:
			for _, l := range lines {
				if l.obj != nil && recentUnread(l.obj, title, s) {
					bump(l.obj)
					l.obj.Set("last_seen", now)
				}
			}
			write(lines)
		case dedup == "info" && count(lines, func(o *ojson.Object) bool { return recentInfo(o, title, s) }) > 0:
			for _, l := range lines {
				if l.obj != nil && recentInfo(l.obj, title, s) {
					bump(l.obj)
					l.obj.Set("last_seen", now)
					l.obj.Set("message", message)
				}
			}
			write(lines)
		default:
			o := ojson.New().Set("id", id).Set("time", now).Set("last_seen", now).Set("count", json.Number("1")).
				Set("status", status).Set("severity", severity).Set("category", category).Set("source", source).
				Set("title", title).Set("message", message)
			if det != nil {
				o.Set("details", det)
			}
			f, err := os.OpenFile(Log, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
			if err == nil {
				f.WriteString(ojson.Compact(o) + "\n")
				f.Close()
			}
			added = true
		}
	})
	return added
}

// Merge rewrites the entry whose details.<key> matches, so a later event updates the earlier one, false when nothing matched
func Merge(key, severity, title, message, details string) bool {
	d, err := ojson.ParseObject(details)
	if err != nil {
		return false
	}
	value, ok := d.Get(key)
	if !ok || value == nil || ojson.Text(value) == "" || value == false {
		return false
	}
	want := ojson.Compact(value)
	initLog()
	now := time.Now().Format(stamp)
	merged := false
	match := func(o *ojson.Object) bool {
		dv, _ := o.Get("details")
		dobj, ok := dv.(*ojson.Object)
		if !ok {
			return false
		}
		v, ok := dobj.Get(key)
		return ok && ojson.Compact(v) == want
	}
	withLock(func() {
		lines := read()
		if count(lines, match) == 0 {
			return
		}
		for _, l := range lines {
			if l.obj == nil || !match(l.obj) {
				continue
			}
			l.obj.Set("title", title).Set("message", message).Set("severity", severity).Set("status", "unread").Set("last_seen", now)
			dv, _ := l.obj.Get("details")
			dobj := dv.(*ojson.Object)
			for _, k := range d.Keys() {
				v, _ := d.Get(k)
				dobj.Set(k, v)
			}
		}
		write(lines)
		merged = true
	})
	return merged
}

// Resolve marks unread non-info entries with this title read, prefix matches dynamic titles, returns the oldest first seen time
func Resolve(title string, prefix bool) (string, bool) {
	match := func(o *ojson.Object) bool {
		if o.Str("status") != "unread" || o.Str("severity") == "info" {
			return false
		}
		if prefix {
			return strings.HasPrefix(o.Str("title"), title)
		}
		return o.Str("title") == title
	}
	initLog()
	if !strings.Contains(conf.Read(Log), `"unread"`) {
		return "", false
	}
	now := time.Now().Format(stamp)
	first := ""
	withLock(func() {
		lines := read()
		if count(lines, match) == 0 {
			return
		}
		for _, l := range lines {
			if l.obj != nil && match(l.obj) {
				if t := l.obj.Str("time"); first == "" || t < first {
					first = t
				}
				l.obj.Set("status", "read").Set("resolved_at", now)
			}
		}
		write(lines)
	})
	return first, first != ""
}

// DeleteTitle drops every entry with this title
func DeleteTitle(title string) {
	initLog()
	withLock(func() {
		var keep []line
		for _, l := range read() {
			if l.obj != nil && l.obj.Str("title") == title {
				continue
			}
			keep = append(keep, l)
		}
		write(keep)
	})
}

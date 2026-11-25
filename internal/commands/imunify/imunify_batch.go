// ##############################################################################
// Script Name: internal/commands/imunify/imunify_batch.go
// Description: Install and manage ImunifyAV service.
// Usage: opencli imunify [status|start|stop|install|update|uninstall|hook]
// Author: Stefan Pejcic
// Created: 25.11.2026
// Last Modified: 25.11.2026
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

package imunify

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/ojson"
	"opencli/internal/sh"
)

const (
	batchFile  = iavSpool + "/batch.json"
	batchLock  = iavSpool + "/batch.lock"
	flushUnit  = "openpanel-imunify-flush"
	flushAfter = 10 * time.Minute
	// no account scan for this long and the next one starts a new batch
	batchGap = 3 * time.Hour
)

// one pass of the scheduled scan over all accounts
type batch struct {
	ID       string   `json:"id"`
	Start    int64    `json:"start"`
	End      int64    `json:"end"`
	Last     int64    `json:"last"`
	Accounts []string `json:"accounts"`
	Running  []string `json:"running"`
	Issues   []string `json:"issues"`
	Files    int64    `json:"files"`
	Sent     bool     `json:"sent"`
}

func lockBatch() func() {
	f, err := os.OpenFile(batchLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	return func() { f.Close() }
}

func loadBatch() *batch {
	var b batch
	if raw, err := os.ReadFile(batchFile); err != nil || json.Unmarshal(raw, &b) != nil || b.ID == "" {
		return nil
	}
	return &b
}

func saveBatch(b *batch) {
	raw, _ := json.Marshal(b)
	os.WriteFile(batchFile, raw, 0o600)
}

// a sent batch is done, so is one that went quiet, unless an account scan is still running and not stale
func (b *batch) open(now time.Time) bool {
	if b == nil || b.Sent {
		return false
	}
	idle := now.Sub(time.Unix(b.Last, 0))
	if len(b.Running) > 0 {
		return idle < 24*time.Hour
	}
	return idle < batchGap
}

func (b *batch) track(ev *ojson.Object, ctx string, issue bool) *batch {
	now := time.Now()
	if !b.open(now) {
		b = &batch{ID: "imunify-" + strconv.FormatInt(now.UnixNano(), 10)}
	}
	b.Last = now.Unix()
	started := int64(math.Floor(num(alt(ev, "started", json.Number("0")))))
	if started > 0 && (b.Start == 0 || started < b.Start) {
		b.Start = started
	}
	b.Running = slices.DeleteFunc(b.Running, func(s string) bool { return s == ctx })
	if endsWith(ev, "SCAN_STARTED") {
		b.Running = append(b.Running, ctx)
		return b
	}
	if c := int64(math.Floor(num(alt(ev, "completed", json.Number("0"))))); c > b.End {
		b.End = c
	}
	if !slices.Contains(b.Accounts, ctx) {
		b.Accounts = append(b.Accounts, ctx)
	}
	b.Files += int64(num(alt(ev, "total_resources", json.Number("0"))))
	if issue && !slices.Contains(b.Issues, ctx) {
		b.Issues = append(b.Issues, ctx)
	}
	return b
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// updates the combined notification in the panel, email and webhook only go out once from the flush
func (b *batch) post(noSend bool) {
	if b == nil {
		return
	}
	n := len(b.Accounts)
	severity, state, title, message := "info", "finished", "", ""
	switch {
	case len(b.Running) > 0:
		state = "running"
		title = "ImunifyAV scan running: " + plural(n+len(b.Running), "account")
		message = fmt.Sprintf("Malware scan is going through the accounts, %s done so far (%d files), now scanning %s. This notification will be updated when the scan finishes.", plural(n, "account"), b.Files, strings.Join(b.Running, ", "))
	default:
		d := int64(0)
		if b.End > b.Start && b.Start > 0 {
			d = b.End - b.Start
		}
		if len(b.Issues) == 0 {
			title = "ImunifyAV scan clean: " + plural(n, "account")
			message = fmt.Sprintf("Malware scan checked %d files in %s in %s, no malware found.", b.Files, plural(n, "account"), humanDuration(d))
		} else {
			severity = "warning"
			title = fmt.Sprintf("ImunifyAV scan finished: %s, %d with issues", plural(n, "account"), len(b.Issues))
			message = fmt.Sprintf("Malware scan checked %d files in %s in %s, %d clean. Malware or errors on: %s, see their own notifications or OpenAdmin > ImunifyAV.", b.Files, plural(n, "account"), humanDuration(d), n-len(b.Issues), strings.Join(b.Issues, ", "))
		}
	}
	details := ojson.New().Set("batch_id", b.ID).Set("accounts", json.Number(strconv.Itoa(n))).
		Set("files", json.Number(strconv.FormatInt(b.Files, 10))).Set("issues", b.Issues).Set("state", state)
	if b.Issues == nil {
		details.Set("issues", []any{})
	}
	args := []string{"sentinel", "--action=imunify_scan", "--title=" + title, "--message=" + message, "--severity=" + severity, "--details=" + ojson.Compact(details), "--merge-key=batch_id"}
	if noSend {
		args = append(args, "--no-send")
	}
	sh.Opencli(args...).Run()
}

// email/webhook for the batch goes out once no account scan followed for a while, each new one pushes it back
func scheduleFlush() {
	if !sh.Has("systemd-run") {
		return
	}
	sh.C("systemctl", "stop", flushUnit+".timer", flushUnit+".service").NoErr().Quiet().Run()
	sh.C("systemctl", "reset-failed", flushUnit+".timer", flushUnit+".service").NoErr().Quiet().Run()
	sh.C("systemd-run", "--unit="+flushUnit, "--collect", "--quiet", "--on-active="+strconv.Itoa(int(flushAfter.Seconds())),
		"/usr/local/bin/opencli", "imunify", "notify", "--flush").NoErr().Quiet().Run()
}

// opencli imunify notify --flush
func flush() {
	unlock := lockBatch()
	defer unlock()
	b := loadBatch()
	if b == nil || b.Sent || len(b.Running) > 0 || time.Since(time.Unix(b.Last, 0)) < flushAfter-time.Minute {
		return
	}
	b.Sent = true
	saveBatch(b)
	b.post(false)
}

// ##############################################################################
// Script Name: internal/commands/email/quotas.go
// Description: Fixes email permission issues for all domains.
// Usage: opencli email-quotas
// Author: Stefan Pejcic
// Created: 03.12.2025
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

package email

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-quotas",
		Desc:  "Fixes email permission issues for all domains.",
		Usage: "opencli email-quotas",
		Run:   quotas,
	})
}

const quotasLog = "/var/log/openpanel/admin/email-quotas.log"

func quotasLogf(format string, a ...any) {
	line := time.Now().Format("2006-01-02 15:04:05") + " : " + fmt.Sprintf(format, a...)
	fmt.Println(line)
	if f, err := os.OpenFile(quotasLog, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		fmt.Fprintln(f, line)
		f.Close()
	} else {
		fmt.Fprintf(os.Stderr, "tee: %s: No such file or directory\n", quotasLog)
	}
}

var whoRe = regexp.MustCompile(`^(\S+) (\S+)$`)

func quotas(args []string) int {
	emailGate()
	v, _ := conf.Raw(conf.AdminINI, "email_storage_location")
	store := text.Xargs(v)
	folder := "/home/*/mail/"
	if strings.HasPrefix(store, "/") {
		folder = store
	}
	folders, _ := filepath.Glob(strings.TrimSuffix(folder, "/") + "/*")
	var list []string
	for _, f := range folders {
		if !strings.HasPrefix(filepath.Base(f), ".") {
			list = append(list, f)
		}
	}
	sort.Strings(list)
	if len(list) == 0 {
		list = []string{strings.TrimSuffix(folder, "/") + "/*"}
	}
	total := len(list)

	quotasLogf("Total mail domains in %s: [%d]", folder, total)
	count := 0
	for _, fn := range list {
		if !conf.IsDir(fn) {
			continue
		}
		count++
		domain := filepath.Base(fn)
		quotasLogf("[*] DOMAIN: %s [%d/%d]", domain, count, total)
		quotasLogf("Checking username and docker context for domain..")
		who, _ := sh.Opencli("domains-whoowns", domain, "--context").Out()
		m := whoRe.FindStringSubmatch(who)
		if m == nil {
			quotasLogf("%s skipped - No owner detected for domain: %s", fn, domain)
			continue
		}
		username, ctx := m[1], m[2]
		var st syscall.Stat_t
		uid := ""
		if syscall.Stat("/home/"+ctx, &st) == nil {
			uid = strconv.Itoa(int(st.Uid))
		}
		if uid == "" {
			quotasLogf("%s skipped - /home/%s not found for domain: %s", fn, ctx, domain)
			continue
		}
		quotasLogf("USERNAME: %s - CONTEXT: %s (UID: %s)", username, ctx, uid)
		quotasLogf("Setting permissions to: %s:%s for all mails in: %s", uid, uid, fn)
		quotasChown(fn, int(st.Uid))
	}
	quotasLogf("Finished processing a total of %d domains.", count)
	return 0
}

// quotasChown is chown -R with a 300s limit, symlinks are not followed
func quotasChown(root string, id int) {
	deadline := time.Now().Add(300 * time.Second)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if time.Now().After(deadline) {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		os.Lchown(p, id, id)
		return nil
	})
}

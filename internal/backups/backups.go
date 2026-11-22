// ##############################################################################
// Script Name: internal/backups/backups.go
// Description: Shared helpers for the read-only mounts of a user's backup service.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 15.10.2026
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

package backups

import (
	"os"
	"regexp"
	"strings"
	"syscall"

	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

// MailDir is the real mail dir of a domain, mail_data only holds a symlink the backup container can't follow
func MailDir(ctx, domain string) string {
	v, _ := conf.Raw(conf.AdminINI, "email_storage_location")
	v = strings.Join(strings.Fields(v), " ")
	if strings.HasPrefix(v, "/") {
		return strings.TrimRight(v, "/") + "/" + domain
	}
	return "/home/" + ctx + "/mail/" + domain
}

// withLock serializes compose edits, domains-add runs the mail and dns steps in parallel
func withLock(ctx string, fn func() bool) bool {
	f, err := os.OpenFile("/tmp/.backup-mounts-"+ctx+".lock", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX) != nil {
		return false
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

var mailMount = regexp.MustCompile(`^      - mail_data:/backup/(01-)?mail:ro$`)

// MountAdd adds a read-only mount to the backup service right after the mail_data one
func MountAdd(ctx, src, dest string) bool {
	compose := podman.ComposeFile(ctx)
	line := "      - " + strings.TrimRight(src, "/") + ":" + dest + ":ro"
	if !conf.IsFile(compose) {
		return false
	}
	return withLock(ctx, func() bool {
		lines := conf.Lines(compose)
		hasMail := false
		for _, l := range lines {
			if l == line {
				return true
			}
			if mailMount.MatchString(l) {
				hasMail = true
			}
		}
		// 01-mail since 2.0.16, mail is the older mount
		if !hasMail {
			return false
		}
		var out []string
		for _, l := range lines {
			out = append(out, l)
			if l == "      - mail_data:/backup/mail:ro" || l == "      - mail_data:/backup/01-mail:ro" {
				out = append(out, line)
			}
		}
		return conf.WriteInPlace(compose, strings.Join(out, "\n")+"\n") == nil
	})
}

// MountRemove removes the mount for dest from the backup service
func MountRemove(ctx, dest string) bool {
	compose := podman.ComposeFile(ctx)
	if !conf.IsFile(compose) {
		return true
	}
	return withLock(ctx, func() bool {
		suffix := ":" + dest + ":ro"
		lines := conf.Lines(compose)
		found := false
		var out []string
		for _, l := range lines {
			if strings.HasSuffix(l, suffix) {
				found = true
			}
			if strings.HasPrefix(l, "      - ") && strings.HasSuffix(l, suffix) {
				continue
			}
			out = append(out, l)
		}
		if !found {
			return true
		}
		return conf.WriteInPlace(compose, strings.Join(out, "\n")+"\n") == nil
	})
}

// ServiceReload recreates a user-configured backup container in the background so it picks up new mounts
func ServiceReload(username, ctx string) bool {
	if conf.Exists("/etc/openpanel/openpanel/core/users/" + username + "/admin.backups") {
		return true
	}
	if podman.User(ctx, "inspect", "-f", "{{.State.Running}}", "backup").NoErr().Str() != "true" {
		return true
	}
	sock, err := podman.Socket(ctx)
	if err != nil {
		return false
	}
	sh.C("sh", "-c", "cd /home/"+ctx+" && CONTAINER_HOST="+sock+" podman-compose up -d --no-deps --force-recreate backup").Quiet().Background()
	return true
}

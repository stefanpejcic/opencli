// ##############################################################################
// Script Name: internal/diskquota/diskquota.go
// Description: Disk and inode limits for an account, user quota plus a project quota on its home.
// Author: Stefan Pejcic
// Created: 24.11.2026
// Last Modified: 24.11.2026
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

// Package diskquota sets the plan's disk and inode limits on an account.
//
// A user quota only counts files owned by the account's own uid, but rootless
// containers write as subuids (mariadb's datadir is 100998 and up, php and
// apache write as www-data's subuid), so those files counted against nothing.
// When the filesystem has project quotas on, the account's home is tagged with
// project id = its uid (inherited by everything created under it, and a process
// in a user namespace can't change it), and the limit goes on that project, so
// every file under /home/<user> counts whichever uid owns it.
package diskquota

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"opencli/internal/sh"
)

// Mount is the filesystem the limits are set on, homes are on /
const Mount = "/"

var (
	projectOnce     sync.Once
	projectOn       bool
	projectEnforced bool
)

func projectState() {
	projectOnce.Do(func() {
		out, _ := sh.C("quotaon", "-pPv", Mount).NoErr().Out()
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if !strings.HasPrefix(l, "project quota on "+Mount+" ") {
				continue
			}
			switch {
			case strings.HasSuffix(l, "is on (enforced)"):
				projectOn, projectEnforced = true, true
			case strings.HasSuffix(l, "is on (accounting)"), strings.HasSuffix(l, "is on"):
				projectOn = true
			}
		}
	})
}

// ProjectOn reports whether project quotas are counted on Mount
func ProjectOn() bool { projectState(); return projectOn }

// Enforce turns enforcement of the counted project quotas on: with the ext4 quota feature
// they are only counted unless / is mounted with prjquota, and that option is left out of
// fstab because a filesystem without the project feature then fails to mount at all
func Enforce() error {
	projectState()
	if !projectOn || projectEnforced {
		return nil
	}
	if out, err := sh.C("quotaon", "-P", Mount).Combined(); err != nil {
		return fmt.Errorf("quotaon -P %s: %v %s", Mount, err, strings.TrimSpace(out))
	}
	projectEnforced = true
	return nil
}

// UID of a linux user, the project id of its home
func UID(username string) (uint32, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, err
	}
	if n < 1000 {
		return 0, fmt.Errorf("refusing project id %d for %s, system uid", n, username)
	}
	return uint32(n), nil
}

// Home of an account
func Home(username string) string { return "/home/" + username }

// struct fsxattr from linux/fs.h
type fsxattr struct {
	xflags     uint32
	extsize    uint32
	nextents   uint32
	projid     uint32
	cowextsize uint32
	pad        [8]byte
}

const (
	fsIocFsgetxattr    = 0x801c581f // _IOR('X', 31, struct fsxattr)
	fsIocFssetxattr    = 0x401c5820 // _IOW('X', 32, struct fsxattr)
	fsXflagProjInherit = 0x00000200
)

func ioctlXattr(fd int, req uintptr, x *fsxattr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(x))); e != 0 {
		return e
	}
	return nil
}

func openNoFollow(path string) (int, error) {
	return syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
}

// project id of a file and whether new files under it inherit it
func getProject(path string) (uint32, bool, error) {
	fd, err := openNoFollow(path)
	if err != nil {
		return 0, false, err
	}
	defer syscall.Close(fd)
	var x fsxattr
	if err := ioctlXattr(fd, fsIocFsgetxattr, &x); err != nil {
		return 0, false, err
	}
	return x.projid, x.xflags&fsXflagProjInherit != 0, nil
}

func setProject(path string, id uint32, dir bool) error {
	fd, err := openNoFollow(path)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var x fsxattr
	if err := ioctlXattr(fd, fsIocFsgetxattr, &x); err != nil {
		return err
	}
	want := x.xflags
	if dir {
		want |= fsXflagProjInherit
	}
	if x.projid == id && x.xflags == want {
		return nil
	}
	x.projid, x.xflags = id, want
	return ioctlXattr(fd, fsIocFssetxattr, &x)
}

// Tagged reports whether dir already carries project id and passes it on to new files
func Tagged(dir string, id uint32) bool {
	p, inherit, err := getProject(dir)
	return err == nil && p == id && inherit
}

// Tag sets project id on dir and everything under it on the same filesystem,
// directories also get the inherit flag so whatever is created later gets it too.
// Symlinks and sockets can't be opened, they stay on project 0 (an inode each, no blocks worth counting).
// Other mounts under it (overlay of a running container) are skipped, their files live in docker-data/ anyway.
func Tag(dir string, id uint32) (tagged int, failed int, err error) {
	var root syscall.Stat_t
	if err := syscall.Lstat(dir, &root); err != nil {
		return 0, 0, err
	}
	if root.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return 0, 0, fmt.Errorf("%s is not a directory", dir)
	}
	// the top directory first, files created during the walk then inherit it already
	if err := setProject(dir, id, true); err != nil {
		return 0, 0, err
	}
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// vanished while walking (tmp files, container churn) or unreadable, keep going
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == dir {
			return nil
		}
		var st syscall.Stat_t
		if syscall.Lstat(path, &st) != nil {
			return nil
		}
		mode := st.Mode & syscall.S_IFMT
		if st.Dev != root.Dev {
			if mode == syscall.S_IFDIR {
				return fs.SkipDir
			}
			return nil
		}
		switch mode {
		case syscall.S_IFDIR, syscall.S_IFREG, syscall.S_IFIFO:
		default:
			return nil
		}
		if setProject(path, id, mode == syscall.S_IFDIR) == nil {
			tagged++
		} else {
			failed++
		}
		return nil
	})
	return tagged, failed, walkErr
}

// Set applies the limits (1K blocks, inodes, 0 is unlimited) to the account:
// always as a user quota like before, and with project quotas on also on the
// project of its home, which is tagged first when it isn't yet (new account,
// restored or recreated home). The returned string says what was done.
func Set(username, blocks, inodes string) (string, error) {
	if out, err := sh.C("setquota", "-u", username, blocks, blocks, inodes, inodes, Mount).Combined(); err != nil {
		return "", fmt.Errorf("setquota -u %s: %v %s", username, err, strings.TrimSpace(out))
	}
	if !ProjectOn() {
		return "user quota", nil
	}
	id, err := UID(username)
	if err != nil {
		return "user quota", err
	}
	note := "user and project quota"
	home := Home(username)
	if st, err := os.Stat(home); err == nil && st.IsDir() && !Tagged(home, id) {
		n, failed, err := Tag(home, id)
		if err != nil {
			return "user quota", fmt.Errorf("tagging %s with project %d: %v", home, id, err)
		}
		note += fmt.Sprintf(", tagged %d files in %s", n, home)
		if failed > 0 {
			note += fmt.Sprintf(" (%d could not be tagged)", failed)
		}
	}
	if err := Enforce(); err != nil {
		return "user quota", err
	}
	idS := strconv.FormatUint(uint64(id), 10)
	if out, err := sh.C("setquota", "-P", idS, blocks, blocks, inodes, inodes, Mount).Combined(); err != nil {
		return "user quota", fmt.Errorf("setquota -P %s: %v %s", idS, err, strings.TrimSpace(out))
	}
	return note, nil
}

// Row is one line of repquota: 1K blocks used/soft/hard and inodes used/soft/hard
type Row struct {
	DiskUsed, DiskSoft, DiskHard       string
	InodesUsed, InodesSoft, InodesHard string
}

// ParseLine reads one repquota line, the name is a username, or #id with -n
func ParseLine(l string) (string, Row, bool) {
	if l == "" || l[0] == '*' || strings.HasPrefix(l, "Block") || strings.HasPrefix(l, "---") {
		return "", Row{}, false
	}
	f := strings.Fields(l)
	if len(f) < 8 || len(f[1]) != 2 || strings.Trim(f[1], "+-") != "" {
		return "", Row{}, false
	}
	// a grace column only appears after the block or inode limits that are over the soft limit
	i := 5
	if f[1][0] == '+' {
		i = 6
	}
	if len(f) < i+3 {
		return "", Row{}, false
	}
	return f[0], Row{f[2], f[3], f[4], f[i], f[i+1], f[i+2]}, true
}

func parseRepquota(out string) map[string]Row {
	rows := map[string]Row{}
	for _, l := range strings.Split(out, "\n") {
		if name, r, ok := ParseLine(l); ok {
			rows[name] = r
		}
	}
	return rows
}

// ProjectRows is repquota -P by project id, nil without project quotas
func ProjectRows() map[uint32]Row {
	if !ProjectOn() {
		return nil
	}
	out, err := sh.C("repquota", "-P", "-n", Mount).NoErr().Out()
	if err != nil {
		return nil
	}
	rows := map[uint32]Row{}
	for k, r := range parseRepquota(out) {
		if n, err := strconv.ParseUint(strings.TrimPrefix(k, "#"), 10, 32); err == nil {
			rows[uint32(n)] = r
		}
	}
	return rows
}

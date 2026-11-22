// ##############################################################################
// Script Name: internal/commands/files/fix_permissions.go
// Description: Fix permissions for users /home directory files inside the container.
// Usage: opencli files-fix_permissions <USERNAME|--all> [PATH] [--debug]
// Author: Stefan Pejcic
// Created: 15.11.2023
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

package files

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "files-fix_permissions",
		Desc:  "Fix permissions for users /home directory files inside the container.",
		Usage: "opencli files-fix_permissions <USERNAME|--all> [PATH] [--debug]",
		Run:   fixPermissions,
	})
}

var fixWebserver = regexp.MustCompilePOSIX(`nginx|openresty|apache|openlitespeed|litespeed`)

func fixPermissions(args []string) int {
	db.Require()
	verbose := false
	var rest []string
	for _, a := range args {
		if a == "--debug" {
			verbose = true
		} else {
			rest = append(rest, a)
		}
	}
	if cli.Arg(rest, 0) == "--all" {
		failed := false
		out, _ := sh.Opencli("user-list", "--local", "--json").Out()
		for _, u := range fixUsernames(out) {
			if fixApply(u, "", verbose) != 0 {
				failed = true
			}
		}
		if failed {
			return 1
		}
		return 0
	}
	username := cli.Arg(rest, 0)
	path := cli.Arg(rest, 1)
	if username == "" {
		fmt.Println("Username required")
		return 1
	}
	if fixApply(username, path, verbose) != 0 {
		return 1
	}
	return 0
}

// fixUsernames is the jq select of non suspended usernames from user-list --json
func fixUsernames(out string) []string {
	var names []string
	v, err := ojson.Parse(out)
	if err != nil {
		return nil
	}
	top, ok := v.(*ojson.Object)
	if !ok {
		return nil
	}
	data, _ := top.Get("data")
	list, _ := data.([]any)
	for _, it := range list {
		o, ok := it.(*ojson.Object)
		if !ok {
			continue
		}
		u := o.Str("username")
		if !strings.HasPrefix(u, "SUSPENDED_") {
			names = append(names, strings.Fields(u)...)
		}
	}
	return names
}

func fixApply(username, path string, verbose bool) int {
	info, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+db.Escape(username)+"';", "-s").Out()
	context := ""
	if f := strings.Fields(info); len(f) >= 2 {
		context = f[1]
	}
	if context == "" {
		fmt.Println("FATAL ERROR: user " + username + " does not have a valid docker context.")
		cli.Exit(1)
	}

	var directory, fake string
	if path != "" {
		path = strings.TrimPrefix(path, "/var/www/html/")
		directory = "/home/" + context + "/docker-data/volumes/" + context + "_html_data/_data/" + path
		fake = "/var/www/html/" + path
	} else {
		directory = "/home/" + context + "/docker-data/volumes/" + context + "_html_data/_data/"
		fake = "/var/www/html/"
	}

	uid := ""
	if st, err := os.Stat("/home/" + context); err == nil {
		uid = strconv.Itoa(int(st.Sys().(*syscall.Stat_t).Uid))
	}
	gid := uid
	if g, ok := fixLitespeedGid(context, uid); ok {
		gid = g
	}

	root := tenantfs.Volume(context, "html_data")
	ownerOK := tenantfs.ChownR(root, directory, uid+":"+gid) == nil
	filesOK := fixChmodWalk(root, directory, false, 0o664, verbose)
	foldersOK := fixChmodWalk(root, directory, true, 0o775, verbose)
	ftpOK := fixFTP(username, uid)

	if ownerOK && filesOK && foldersOK && ftpOK {
		fmt.Println("Permissions applied successfully to " + fake)
		return 0
	}
	fmt.Println("Error applying permissions to " + fake)
	return 1
}

// fixLitespeedGid is the nogroup workaround, ok is false when the gid stays the uid
func fixLitespeedGid(context, uid string) (string, bool) {
	env := "/home/" + context + "/.env"
	raw := ""
	if !conf.IsFile(env) {
		fmt.Fprintf(os.Stderr, "grep: %s: No such file or directory\n", env)
	} else {
		raw, _ = conf.Raw(env, "WEB_SERVER")
	}
	// awk -F= '{print $2}' only keeps the text up to the next =
	if i := strings.IndexByte(raw, '='); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.Join(strings.Fields(raw), "")
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	ws := fixWebserver.FindString(raw)
	if ws != "openlitespeed" && ws != "litespeed" {
		return "", false
	}
	if g := fixMappedGid(ws, uid); g != "" {
		return g, true
	}
	fmt.Fprintf(os.Stderr, "WARNING: could not determine %s's real nogroup gid (is the '%s' container running?) - falling back to 65534, which is likely wrong. Run 'opencli files-fix_permissions %s' again once the container is up.\n", context, ws, context)
	return "65534", true
}

// fixMappedGid is the host gid behind nogroup (65534) in the container's gid_map
func fixMappedGid(container, uid string) string {
	if uid == "" {
		return ""
	}
	gm, _ := sh.C("podman", "--remote", "exec", container, "cat", "/proc/self/gid_map").
		Env("CONTAINER_HOST=unix:///hostfs/run/user/" + uid + "/podman/podman.sock").NoErr().Out()
	for _, line := range strings.Split(gm, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		in, _ := strconv.ParseInt(f[0], 10, 64)
		host, _ := strconv.ParseInt(f[1], 10, 64)
		n, _ := strconv.ParseInt(f[2], 10, 64)
		if 65534 >= in && 65534 < in+n {
			return strconv.FormatInt(host+(65534-in), 10)
		}
	}
	return ""
}

// fixChmodWalk is find . -type f|d -execdir chmod -h <mode> inside the checked dir, true when nothing failed
func fixChmodWalk(root, dir string, dirs bool, mode fs.FileMode, verbose bool) bool {
	real, err := tenantfs.Resolve(root, dir)
	if err != nil {
		return false
	}
	// --debug wants chmod's own -v lines
	if verbose && dirs {
		return sh.C("find", ".", "-type", "d", "-execdir", "chmod", "-h", "-v", strconv.FormatInt(int64(mode), 8), "{}", "+").Dir(real).Run() == nil
	}
	r, err := os.OpenRoot(real)
	if err != nil {
		return false
	}
	defer r.Close()
	ok := true
	fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			ok = false
			return nil
		}
		if d.IsDir() != dirs || (!d.IsDir() && !d.Type().IsRegular()) {
			return nil
		}
		st, err := r.Lstat(p)
		if err != nil {
			ok = false
			return nil
		}
		m := mode
		if dirs {
			m, _ = tenantfs.ApplyMode(st.Mode(), strconv.FormatInt(int64(mode), 8))
		}
		if r.Chmod(p, m) != nil {
			ok = false
		}
		return nil
	})
	return ok
}

// fixFTP returns the status of the last command like the function did
func fixFTP(user, uid string) bool {
	base := "/home/" + user + "/docker-data/volumes/" + user + "_html_data/_data"
	found := false
	out, _ := sh.Opencli("ftp-list", user).Out()
	lines := strings.Split(out, "\n")
	lines = lines[1:]
	for _, line := range lines {
		field := line
		if i := strings.IndexByte(line, '|'); i >= 0 {
			field = line[i+1:]
			if j := strings.IndexByte(field, '|'); j >= 0 {
				field = field[:j]
			}
		}
		directory := strings.Trim(field, " ")
		if directory == "/var/www/html" || directory == "" {
			continue
		}
		found = true
		rel := strings.TrimPrefix(directory, "/var/www/html/")
		rel = strings.TrimPrefix(rel, "/var/www/html")
		if rel == "" {
			continue
		}
		nd := base + "/" + rel
		if conf.IsDir(nd) {
			fmt.Println("[*] Fixing ownership for FTP path: " + nd)
			tenantfs.ChownR(base, nd, uid+":"+uid)
		}
	}
	if !found {
		return true
	}
	ok := true
	m := syscall.Umask(0)
	syscall.Umask(m)
	home := "/home/" + user
	for _, p := range []string{home, home + "/docker-data", home + "/docker-data/volumes", home + "/docker-data/volumes/" + user + "_html_data", base} {
		st, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "chmod: cannot access '%s': No such file or directory\n", p)
			ok = false
			continue
		}
		if err := os.Chmod(p, st.Mode()|fs.FileMode(0o555&^m)); err != nil {
			fmt.Fprintf(os.Stderr, "chmod: changing permissions of '%s': %v\n", p, err)
			ok = false
		}
	}
	return ok
}

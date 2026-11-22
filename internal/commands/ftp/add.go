// ##############################################################################
// Script Name: internal/commands/ftp/add.go
// Description: Create FTP sub-user for openpanel user.
// Usage: opencli ftp-add <NEW_USERNAME> <NEW_PASSWORD> <FOLDER> <OPENPANEL_USERNAME> [--quota=<MB>] [--debug]
// Author: Stefan Pejcic
// Created: 22.05.2024
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

package ftp

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "ftp-add",
		Desc:  "Create FTP sub-user for openpanel user.",
		Usage: "opencli ftp-add <NEW_USERNAME> <NEW_PASSWORD> <FOLDER> <OPENPANEL_USERNAME> [--quota=<MB>] [--debug]",
		Docs:  "https://docs.openpanel.com",
		Run:   add,
	})
}

var (
	addLocalRe = regexp.MustCompile(`^[a-z0-9._-]+$`)
	addOpRe    = regexp.MustCompile(`^[a-z0-9_-]+$`)
	addQuotaRe = regexp.MustCompile(`^([0-9]+[MG]?|UNLIMITED)$`)
	addDirRe   = regexp.MustCompile(`^/var/www/html/[A-Za-z0-9._/-]*$`)
	addGlobRe  = regexp.MustCompile(`[*?\\\[]\]`)
)

func add(args []string) int {
	if len(args) < 4 || len(args) > 6 {
		fmt.Println("Usage: opencli ftp-add <new_username> <new_password> '<directory>' <openpanel_username> [--quota=<MB>] [--debug]")
		return 1
	}
	username := strings.ToLower(args[0])
	password := args[1]
	directory := args[2]
	opUser := args[3]
	debug := false
	quota := ""
	for _, a := range args {
		if a == "--debug" {
			debug = true
		} else if strings.HasPrefix(a, "--quota=") {
			quota = strings.TrimPrefix(a, "--quota=")
		}
	}

	ctx := addValidate(username, password, directory, opUser, quota)
	usersList := mkUsersList(ctx)
	if hasUser(usersList, username) {
		cli.Fail("ERROR: FTP User '%s' already exists.", username)
	}
	if !podman.IsRunning("openadmin_ftp") {
		podman.EnsureRunning("openadmin_ftp", "/root", "openadmin_ftp", 0)
	}
	addCreate(ctx, username, password, directory, opUser, quota, usersList, debug)
	return 0
}

func addValidate(username, password, directory, opUser, quota string) string {
	if !strings.Contains(username, "@") {
		cli.Fail("ERROR: FTP username must be in format: username@domain")
	}
	local := username[:strings.Index(username, "@")]
	domain := username[strings.LastIndex(username, "@")+1:]
	if !addLocalRe.MatchString(local) {
		cli.Fail("ERROR: FTP username before the domain part may only contain a-z, 0-9, ., _ and -")
	}
	if !addLocalRe.MatchString(domain) {
		cli.Fail("ERROR: Invalid domain provided in FTP username.")
	}
	if !addOpRe.MatchString(opUser) {
		cli.Fail("ERROR: Invalid OpenPanel username.")
	}
	if quota != "" && !addQuotaRe.MatchString(strings.ToUpper(quota)) {
		cli.Fail("ERROR: Invalid quota '%s'. Use a number of MB (500), a value with a unit (500M, 2G), or 'unlimited'.", quota)
	}
	ctx := contextFor(opUser, "x@"+domain)
	if !strings.HasPrefix(directory, "/var/www/html/") {
		cli.Fail("ERROR: Invalid path. It must start with /var/www/html/")
	}
	if addGlobRe.MatchString(directory) {
		cli.Fail("ERROR: Path cannot contain glob characters.")
	}
	if !addDirRe.MatchString(directory) {
		cli.Fail("ERROR: The path can only contain letters, numbers, dots, dashes, underscores and slashes.")
	}
	if strings.Contains(directory, "..") {
		cli.Fail("ERROR: Path traversal detected (.. is not allowed).")
	}
	if strings.Contains(directory, "//") {
		cli.Fail("ERROR: Double slashes are not allowed in the path.")
	}
	if strings.Contains(directory, "|") {
		cli.Fail("ERROR: The path cannot contain the '|' character.")
	}
	checkPassword(password, "")
	return ctx
}

func addCreate(ctx, username, password, directory, opUser, quota, usersList string, debug bool) {
	realPath := "/home/" + ctx + "/docker-data/volumes/" + ctx + "_html_data/_data/"
	rel := strings.TrimPrefix(directory, "/var/www/html/")
	newDir := realPath + rel
	root := strings.TrimSuffix(realPath, "/")

	var gid string
	var st syscall.Stat_t
	if err := syscall.Stat("/home/"+ctx, &st); err != nil {
		fmt.Fprintf(os.Stderr, "stat: cannot statx '/home/%s': No such file or directory\n", ctx)
	} else {
		gid = strconv.Itoa(int(st.Uid))
	}
	if gid == "" {
		cli.Fail("ERROR: Could not determine GID for '%s'.", ctx)
	}

	existing := sh.C("podman", "exec", "openadmin_ftp", "getent", "group", gid).NoErr().Str()
	if i := strings.Index(existing, ":"); i >= 0 {
		existing = existing[:i]
	}
	if existing == "" {
		ftpExec("addgroup", "-g", gid, ctx).Run()
	}

	if tenantfs.Mkdir(root, newDir) != nil {
		cli.Fail("ERROR: Directory '%s' is not a real folder inside /var/www/html/.", directory)
	}
	addChmods(ctx)

	hashed := hashPassword(password, true)
	ftpExec("useradd", "-d", newDir, "-s", "/sbin/nologin", "-g", ctx, "-M", username, "--badname").Run()

	if ftpExec("usermod", "-p", hashed, username).Run() == nil {
		tenantfs.ChownR(root, newDir, gid+":"+gid)
		tenantfs.ChmodR(root, newDir, "2775")
		addChmods(ctx)

		uid := ftpExec("id", "-u", username).Str()
		ugid := ftpExec("id", "-g", username).Str()

		if f, err := os.OpenFile(usersList, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			fmt.Fprintf(f, "%s|%s|%s|%s|%s\n", username, hashed, directory, uid, ugid)
			f.Close()
		}

		// ftp-quota also refreshes all.users, otherwise do it here so OpenAdmin lists the new account
		if quota != "" {
			out, _ := sh.Opencli("ftp-quota", username, quota, opUser).Raw()
			fmt.Print(regexp.MustCompile(`(?m)^Success: `).ReplaceAllString(out, "    "))
		} else {
			sh.Opencli("ftp-users").Quiet().Run()
		}

		sentinelBg("--action=ftp_create", "--title=FTP account created", "--message=New FTP account has been created for OpenPanel user: '"+opUser+"'. Directory: "+directory+" | UID: "+uid+" | GID: "+ugid)
		fmt.Printf("Success: FTP user '%s' created successfully (UID: %s, GID: %s).\n", username, uid, ugid)
	} else {
		if debug {
			fmt.Println("ERROR: Failed to create FTP user with command:")
			fmt.Printf("podman exec openadmin_ftp useradd -d %s -s /sbin/nologin -g %s %s\n", newDir, ctx, username)
		} else {
			fmt.Printf("ERROR: Failed to create FTP user. To debug, run: opencli ftp-add %s %s '%s' %s --debug\n", username, password, newDir, ctx)
		}
		cli.Exit(1)
	}
}

func addChmods(ctx string) {
	base := "/home/" + ctx
	for _, p := range []string{base, base + "/docker-data", base + "/docker-data/volumes", base + "/docker-data/volumes/" + ctx + "_html_data", base + "/docker-data/volumes/" + ctx + "_html_data/_data"} {
		chmodPlusRX(p)
	}
}

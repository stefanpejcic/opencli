// ##############################################################################
// Script Name: internal/commands/user/suspend.go
// Description: Suspend user: stop all containers and suspend domains.
// Usage: opencli user-suspend <USERNAME> [-y] [--debug]
// Author: Stefan Pejcic
// Created: 01.10.2023
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

package user

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-suspend",
		Desc:  "Suspend user: stop all containers and suspend domains.",
		Usage: "opencli user-suspend <USERNAME> [-y] [--debug]",
		Run:   suspend,
	})
}

const (
	suspendedDir  = "/etc/openpanel/caddy/suspended_domains/"
	suspendTpl    = "/etc/openpanel/caddy/templates/suspended_user.conf"
	caddyVhostDir = "/etc/openpanel/caddy/domains"
)

var suspendMarked = regexp.MustCompile(`^# suspended_by:|suspended_domain\.html`)

type suspendCtx struct {
	username string
	debug    bool
	userID   string
	context  string
}

func suspend(args []string) int {
	if len(args) < 1 || len(args) > 4 {
		fmt.Println("Usage: opencli user-suspend <username> [-y] [--debug]")
		return 1
	}
	s := &suspendCtx{username: args[0]}
	skip := false
	for _, a := range args[1:] {
		switch a {
		case "--debug":
			s.debug = true
		case "-y":
			skip = true
		default:
			fmt.Println("Unknown option: " + a)
			return 1
		}
	}
	if !skip {
		ans := readPrompt("Are you sure you want to suspend OpenPanel user '" + s.username + "'? (y/N) ")
		switch ans {
		case "y", "Y", "yes", "YES", "Yes", "yEs", "yeS", "YEs", "YeS", "yES":
		default:
			fmt.Println("Operation cancelled.")
			return 1
		}
	}
	db.Require()

	s.getContext()
	s.suspendDomains()
	s.stopContainers()
	s.renameInDB()
	return 0
}

func (s *suspendCtx) getContext() {
	info, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+db.Escape(s.username)+"';", "-s").Out()
	// awk takes the first two fields of each line, only one row is expected
	f := strings.Fields(strings.SplitN(info, "\n", 2)[0])
	if len(f) > 0 {
		s.userID = f[0]
	}
	if len(f) > 1 {
		s.context = f[1]
	}
	if s.userID == "" || s.context == "" {
		cli.Failerr("Error: No username '%s'", s.username)
	}
}

func (s *suspendCtx) suspendDomains() {
	os.MkdirAll(suspendedDir, 0o755)
	list, _ := sh.Opencli("domains-user", s.username).NoErr().Out()
	if list == "" || strings.HasPrefix(list, "User") || strings.HasPrefix(list, "No domains found") {
		fmt.Printf("No domains found for user '%s'.\n", s.username)
		return
	}
	for _, d := range strings.Fields(list) {
		if !strings.Contains(d, ".") {
			fmt.Println("Skipping invalid domain name: " + d)
			continue
		}
		src := caddyVhostDir + "/" + d + ".conf"
		dest := suspendedDir + "/" + d + ".conf"
		// individually suspended domains keep their own page so unsuspending the account can't reactivate them
		if suspendFileMarked(src) {
			continue
		}
		if conf := fileExists(src); conf {
			if !fileExists(dest) {
				copyFile(src, dest)
			}
		}
		tpl, err := os.ReadFile(suspendTpl)
		if err != nil {
			cli.Errf("sed: can't read %s: No such file or directory", suspendTpl)
		}
		out := ""
		if err == nil {
			out = strings.ReplaceAll(string(tpl), "<DOMAIN_NAME>", d)
		}
		os.WriteFile(src, []byte(out), 0o644)
	}
	reloadCaddyBg()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func suspendFileMarked(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if suspendMarked.MatchString(l) {
			return true
		}
	}
	return false
}

func (s *suspendCtx) stopContainers() {
	namesFile := "/home/" + s.context + "/stopped_containers_on_suspend.txt"
	jobs := runtime.NumCPU() * 2
	if s.debug {
		fmt.Println("Stopping containers for user: " + s.username)
	}
	running, _ := podman.User(s.context, "ps", "-a", "--format", "{{.Names}}").Out()
	if running == "" {
		if s.debug {
			fmt.Println("No running containers for " + s.username)
		}
		os.WriteFile(namesFile, nil, 0o644)
		return
	}
	os.WriteFile(namesFile, []byte(running+"\n"), 0o644)
	if s.debug {
		fmt.Println("Saved stopped containers to " + namesFile)
	}
	sock, _ := podman.Socket(s.context)
	var names []string
	for _, l := range strings.Split(running, "\n") {
		if strings.TrimSpace(l) != "" {
			names = append(names, l)
		}
	}
	forEachPar(names, jobs, func(n string) {
		sh.C("podman", "--remote", "stop", n).Env("CONTAINER_HOST=" + sock).Quiet().Run()
	})
}

func (s *suspendCtx) renameInDB() {
	newName := "SUSPENDED_" + time.Now().Format("20060102150405") + "_" + s.username
	q := "UPDATE users SET username='" + db.Escape(newName) + "' WHERE username='" + db.Escape(s.username) + "';"
	sessions := 0
	if db.Exec(q) != nil {
		cli.Fail("ERROR: Failed to suspend user '%s'.", s.username)
	}
	fmt.Printf("User '%s' suspended successfully.\n", s.username)
	redis.DropUserCache(s.userID, s.username, newName)
	if s.userID != "" {
		scan, _ := redis.CLI("--scan", "--pattern", "session:"+s.userID+":*").Out()
		sessions = countLines(scan)
		redis.DropUserSessions(s.userID)
	}
	sentinelBg("--action=user_status", "--title=User account suspended", fmt.Sprintf("--message=User account '%s' has been suspended. %d session(s) terminated.", s.username, sessions))
}

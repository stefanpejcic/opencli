// ##############################################################################
// Script Name: internal/commands/user/unsuspend.go
// Description: Unsuspend user: start all containers and unsuspend domains
// Usage: opencli user-unsuspend <USERNAME> [--debug]
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

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-unsuspend",
		Desc:  "Unsuspend user: start all containers and unsuspend domains",
		Usage: "opencli user-unsuspend <USERNAME> [--debug]",
		Run:   unsuspend,
	})
}

var unsuspendMarker = regexp.MustCompile(`suspended_user.html`)

type unsuspendCtx struct {
	username string
	debug    bool
	context  string
}

func unsuspend(args []string) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Println("Usage: opencli user-unsuspend <username> [--debug]")
		return 1
	}
	u := &unsuspendCtx{username: args[0], debug: cli.HasFlag(args, "--debug")}
	db.Require()

	// only SUSPENDED_<14 digit timestamp>_<username>, a LIKE here also caught other names ending in it
	q := "SELECT server FROM users WHERE username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '" + db.Escape(u.username) + "' LIMIT 1;"
	u.context, _ = db.Cmd(q, "-N").Out()
	if u.context == "" {
		cli.Failerr("Error: No username '%s'", u.username)
	}
	u.renameInDB()
	u.unsuspendDomains()
	u.startContainers()
	return 0
}

func (u *unsuspendCtx) renameInDB() {
	esc := db.Escape(u.username)
	out, _ := db.Cmd("SELECT id, username FROM users WHERE username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '"+esc+"' LIMIT 1;", "-N", "-s").Out()
	row := strings.TrimLeft(strings.SplitN(out, "\n", 2)[0], " \t")
	id, suspended := row, ""
	if i := strings.IndexAny(row, " \t"); i >= 0 {
		id, suspended = row[:i], strings.Trim(row[i:], " \t")
	}
	if id == "" {
		cli.Failerr("ERROR: No suspended user '%s'.", u.username)
	}
	// rename only the row found above
	if db.Exec("UPDATE users SET username='"+esc+"' WHERE id='"+id+"';") != nil {
		cli.Fail("ERROR: Failed to unsuspend user '%s'.", u.username)
	}
	fmt.Printf("User '%s' unsuspended successfully.\n", u.username)
	redis.DropUserCache(id, suspended, u.username)
	sentinelBg("--action=user_status", "--title=User account unsuspended", "--message=User account '"+u.username+"' has been unsuspended.")
}

func (u *unsuspendCtx) unsuspendDomains() {
	list, _ := sh.Opencli("domains-user", u.username).Out()
	for _, d := range strings.Fields(list) {
		// only undo what account suspend did, domain suspensions stay
		b, err := os.ReadFile(caddyVhostDir + "/" + d + ".conf")
		if err != nil || !unsuspendMarker.Match(b) {
			continue
		}
		if fileExists(suspendedDir + d + ".conf") {
			copyFile(suspendedDir+d+".conf", caddyVhostDir+"/"+d+".conf")
		}
	}
	reloadCaddyBg()
}

func (u *unsuspendCtx) startContainers() {
	namesFile := "/home/" + u.context + "/stopped_containers_on_suspend.txt"
	jobs := runtime.NumCPU() * 2
	if u.debug {
		fmt.Println("Starting containers for user: " + u.username)
	}
	sock, _ := podman.Socket(u.context)
	data, err := os.ReadFile(namesFile)
	if err != nil || len(data) == 0 {
		if u.debug {
			fmt.Println("No saved containers to start for " + u.username)
		}
		// fallback for <1.7.51
		all, _ := podman.User(u.context, "ps", "-a", "--format", "{{.Names}}").Out()
		forEachPar(strings.Fields(all), jobs, func(n string) {
			if u.debug {
				fmt.Println("- Starting container: " + n)
			}
			sh.C("podman", "--remote", "start", n).Env("CONTAINER_HOST=" + sock).Quiet().Run()
		})
		podman.SyncVarnish(u.context)
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, c := range lines {
		if c == "" {
			continue
		}
		if podman.User(u.context, "inspect", c).Quiet().OK() {
			if u.debug {
				fmt.Println("Starting container: " + c)
			}
			podman.User(u.context, "start", c).Quiet().Run()
		} else {
			// no container, run compose up instead
			if u.debug {
				fmt.Println("Container " + c + " not found, running compose up")
			}
			podman.ComposeUser(u.context, "up", "-d", c).Dir("/home/" + u.context).Quiet().Run()
		}
	}
	os.Remove(namesFile)
	if u.debug {
		fmt.Println("Cleared " + namesFile)
	}
	// containers come back in saved order, varnish may have started before the webserver got its new IP
	podman.SyncVarnish(u.context)
}

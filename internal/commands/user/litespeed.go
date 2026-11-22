// ##############################################################################
// Script Name: internal/commands/user/litespeed.go
// Description: Show or set the LiteSpeed Enterprise license of a user.
// Usage: opencli user-litespeed <USERNAME> [status|serial <SERIAL>|trial] [--json]
// Author: Stefan Pejcic
// Created: 31.10.2026
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
	"regexp"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-litespeed",
		Desc:  "Show or set the LiteSpeed Enterprise license of a user.",
		Usage: "opencli user-litespeed <USERNAME> [status|serial <SERIAL>|trial] [--json]",
		Run:   litespeed,
	})
}

const lsConf = "/usr/local/lsws/conf"

type lsCtx struct {
	json     bool
	username string
	serial   string
}

func lsUsage() {
	fmt.Println("Usage: opencli user-litespeed <USERNAME> [status|serial <SERIAL>|trial] [--json]")
	fmt.Println()
	fmt.Println("  status           Show the license LiteSpeed runs with (default).")
	fmt.Println("  serial <SERIAL>  Register a LiteSpeed serial number, it's used from then on.")
	fmt.Println("  trial            Remove the serial and go back to a trial license.")
	fmt.Println("  --json           Print the result as JSON.")
	cli.Exit(1)
}

func (l *lsCtx) die(msg string) {
	if l.json {
		fmt.Println(ojson.Compact(ojson.New().Set("success", false).Set("error", msg)))
	} else {
		cli.Errf("Error: %s", msg)
	}
	cli.Exit(1)
}

func (l *lsCtx) lsws(args ...string) *sh.Cmd {
	return podman.Ctx(l.username, append([]string{"exec", "-i", "litespeed"}, args...)...)
}

func litespeed(args []string) int {
	l := &lsCtx{}
	var rest []string
	for _, a := range args {
		switch a {
		case "--json":
			l.json = true
		case "-h", "--help":
			lsUsage()
		default:
			rest = append(rest, a)
		}
	}
	l.username = cli.Arg(rest, 0)
	action := cli.Arg(rest, 1)
	if action == "" {
		action = "status"
	}
	l.serial = cli.Arg(rest, 2)
	if l.username == "" {
		lsUsage()
	}
	envFile := "/home/" + l.username + "/.env"
	if !conf.IsFile(envFile) {
		l.die("User " + l.username + " not found.")
	}
	ws := ""
	for _, ln := range conf.Lines(envFile) {
		if v, ok := strings.CutPrefix(ln, "WEB_SERVER="); ok {
			ws += strings.Map(func(r rune) rune {
				if r == '"' || r == ' ' || (r >= '\t' && r <= '\r') {
					return -1
				}
				return r
			}, v)
		}
	}
	if ws != "litespeed" {
		none := ws
		if none == "" {
			none = "none"
		}
		l.die("User " + l.username + " doesn't use LiteSpeed (webserver: " + none + ").")
	}

	switch action {
	case "status":
		return l.status()
	case "serial":
		if l.serial == "" {
			lsUsage()
		}
		l.setSerial()
	case "trial":
		l.setTrial()
	default:
		lsUsage()
	}
	return 0
}

func (l *lsCtx) ensureRunning() {
	if podman.CtxIsRunning(l.username, "litespeed") {
		return
	}
	podman.ComposeUser(l.username, "up", "-d", "litespeed").Dir("/home/" + l.username).Quiet().Run()
	for i := 1; i <= 30; i++ {
		if podman.CtxIsRunning(l.username, "litespeed") {
			podman.SyncVarnish(l.username)
			return
		}
		time.Sleep(2 * time.Second)
	}
	l.die("LiteSpeed container of " + l.username + " is not running.")
}

var (
	lsLicense = regexp.MustCompile(`(?i)license|expire|serial`)
	lsDays    = regexp.MustCompile(`expire in [0-9]+ day`)
	lsNum     = regexp.MustCompile(`[0-9]+`)
	lsReason  = regexp.MustCompile(`ERR [A-Z.]+: .*`)
)

// prints the license type and what lshttpd says about it
func (l *lsCtx) status() int {
	l.ensureRunning()
	typ, _ := l.lsws("sh", "-c", "if [ -s "+lsConf+"/serial.no ]; then echo serial; elif [ -e "+lsConf+"/license.key ]; then echo license; elif [ -e "+lsConf+"/trial.key ]; then echo trial; else echo none; fi").Out()
	v, _ := l.lsws("/usr/local/lsws/bin/lshttpd", "-V").Combined()
	msg := ""
	for _, ln := range strings.Split(v, "\n") {
		if !strings.Contains(ln, "Memory size") && lsLicense.MatchString(ln) {
			msg = ln
		}
	}
	if i := strings.LastIndex(msg, "] "); i >= 0 {
		msg = msg[i+2:]
	}
	// a serial that never got registered leaves no license key behind
	if typ == "serial" && strings.Contains(msg, "Failed to find your license key") {
		msg = "The serial number isn't registered, LiteSpeed won't start until it is."
	}
	days := ""
	if m := lsDays.FindString(msg); m != "" {
		days = lsNum.FindString(m)
	}
	if l.json {
		o := ojson.New().Set("success", true).Set("type", typ).Set("message", msg)
		if days == "" {
			o.Set("days_left", nil)
		} else {
			n, _ := strconv.Atoi(days)
			o.Set("days_left", n)
		}
		fmt.Println(ojson.Compact(o))
		return 0
	}
	fmt.Println("License: " + typ)
	if msg != "" {
		fmt.Println(msg)
		return 0
	}
	return 1
}

func (l *lsCtx) restart() {
	l.lsws("/usr/local/lsws/bin/lswsctrl", "restart").Quiet().Run()
}

func (l *lsCtx) msg(m string) {
	if l.json {
		fmt.Println(ojson.Compact(ojson.New().Set("success", true).Set("message", m)))
	} else {
		fmt.Println(m)
	}
}

var lsSerialRe = regexp.MustCompile(`^[A-Za-z0-9+/=-]{8,64}$`)

func (l *lsCtx) setSerial() {
	if !lsSerialRe.MatchString(l.serial) {
		l.die("Invalid serial number.")
	}
	l.ensureRunning()
	// the old keys are kept as .old, same as litespeed's own serialctl.sh
	out, _ := l.lsws("sh", "-c", "cd "+lsConf+" && for f in serial.no license.key trial.key; do [ -e $f ] && mv -f $f $f.old; done; cat > serial.no && chown lsadm:lsadm serial.no && /usr/local/lsws/bin/lshttpd -r 2>&1").Stdin(l.serial + "\n").Out()
	if l.lsws("test", "-e", lsConf+"/license.key").OK() {
		l.restart()
		l.msg("Serial number registered, LiteSpeed restarted with the new license.")
		return
	}
	// the serial stays, the running server keeps its old license until the next restart
	reason := ""
	for _, ln := range strings.Split(out, "\n") {
		if m := lsReason.FindString(ln); m != "" {
			reason = m
		}
	}
	if reason == "" {
		reason = "no license key was issued"
	}
	l.die("Serial number could not be registered: " + reason + ". It is saved and LiteSpeed will use it from the next restart.")
}

func (l *lsCtx) setTrial() {
	l.ensureRunning()
	err := l.lsws("sh", "-c", "cd "+lsConf+" && for f in serial.no license.key; do [ -e $f ] && mv -f $f $f.old; done; rm -f trial.key && wget -q -P "+lsConf+"/ http://license.litespeedtech.com/reseller/trial.key && chown lsadm:lsadm trial.key").Run()
	if err != nil {
		l.die("Could not download a trial license.")
	}
	l.restart()
	l.msg("Switched to a trial license.")
}

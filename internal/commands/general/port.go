// ##############################################################################
// Script Name: internal/commands/general/port.go
// Description: View and change port for accessing openpanel.
// Usage: opencli port [set <port> | default] [--no-restart]
// Author: Stefan Pejcic
// Created: 17.02.2025
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

package general

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/redis"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "port",
		Desc:  "View and change port for accessing openpanel.",
		Usage: "opencli port [set <port> | default] [--no-restart]",
		Run:   port,
	})
}

const (
	portRedirects = "/etc/openpanel/caddy/redirects.conf"
	portProxyFile = "/etc/openpanel/nginx/vhosts/openpanel_proxy.conf"
)

var portEnvRe = regexp.MustCompile(`^PORT="(.*)"`)

func portCurrent() string {
	// used to also require /.dockerenv, but OpenAdmin always runs natively on the host now, so the file's presence alone is the signal
	if conf.Exists("/etc/openpanel/no_port") {
		return "443"
	}
	for _, l := range conf.Lines(conf.RootEnv) {
		if m := portEnvRe.FindStringSubmatch(l); m != nil && m[1] != "" {
			return m[1]
		}
	}
	return ""
}

func portUsage() {
	fmt.Println("Usage:")
	fmt.Println("")
	fmt.Println("opencli port                        - displays current port  ")
	fmt.Println("opencli port set 2090               - set 2090 as port for user panel")
	fmt.Println("opencli port default                - set 2083 as port for user panel")
	fmt.Println("")
	fmt.Println("Add --no-restart to skip restarting services after the change.")
}

var portNumRe = regexp.MustCompile(`:[0-9]+`)

// openFirewallPort adds the port to csf TCP_IN and reloads csf in the background
func openFirewallPort(port string) {
	csf := "/etc/csf/csf.conf"
	if !conf.Exists(csf) {
		return
	}
	in := regexp.MustCompile(`TCP_IN = .*` + port)
	for _, l := range conf.Lines(csf) {
		if in.MatchString(l) {
			return
		}
	}
	re := regexp.MustCompile(`TCP_IN = "(.*)"`)
	sedFile(csf, func(lines []string) []string {
		for i, l := range lines {
			if m := re.FindStringSubmatchIndex(l); m != nil {
				lines[i] = l[:m[0]] + `TCP_IN = "` + l[m[2]:m[3]] + "," + port + `"` + l[m[1]:]
			}
		}
		return lines
	})
	sh.C("csf", "-r").Quiet().Background()
}

// flock serializes edits of /root/docker-compose.yml with the other scripts that rewrite it
func withLock(path string, fn func()) {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err == nil {
		defer f.Close()
		syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	fn()
}

func portUpdate(newPort string, noRestart bool) {
	if portCurrent() == newPort {
		fmt.Printf("Port %s is already set for accessing the user panel.\n", newPort)
		cli.Exit(0)
	}
	sedFile(conf.RootEnv, func(lines []string) []string {
		re := regexp.MustCompile(`^PORT="[^"]*"`)
		for i, l := range lines {
			lines[i] = subFirst(re, l, `PORT="`+newPort+`"`)
		}
		return lines
	})
	sedFile(portRedirects, func(lines []string) []string {
		for i, l := range lines {
			if strings.Contains(l, "redir @openpanel") {
				l = portNumRe.ReplaceAllLiteralString(l, ":"+newPort)
			}
			if strings.Contains(l, "reverse_proxy @mcp") {
				l = subFirst(portNumRe, l, ":"+newPort)
			}
			lines[i] = l
		}
		return lines
	})
	sedFile(portProxyFile, func(lines []string) []string {
		return sedRange(lines, regexp.MustCompile(`# openpanel`), regexp.MustCompile(`# openadmin`), func(l string) string {
			return portNumRe.ReplaceAllLiteralString(l, ":"+newPort)
		})
	})
	redis.DropKey("openpanel_cache_app.get_openpanel_port")
	withLock("/tmp/opencli.root_compose.lock", func() {
		from, to := "2083:2083/tcp", "${PORT}:2083/tcp"
		if newPort == "443" {
			from, to = to, from
		}
		sedFile("/root/docker-compose.yml", func(lines []string) []string {
			for i, l := range lines {
				lines[i] = strings.ReplaceAll(l, from, to)
			}
			return lines
		})
	})
	// firewall and caddy apply without a restart, OpenAdmin passes --no-restart and recreates the container itself
	openFirewallPort(newPort)
	podman.CaddyReload()
	if !noRestart {
		sh.Bash("podman-compose down openpanel && podman-compose up -d openpanel").Dir("/root").Quiet().Background()
	}
	fmt.Printf("%s is now set for accessing the OpenPanel interface.\n", newPort)
}

func port(args []string) int {
	noRestart := cli.HasFlag(args, "--no-restart")
	a1, a2 := cli.Arg(args, 0), cli.Arg(args, 1)
	switch {
	case a1 == "":
		fmt.Println(portCurrent())
	case a1 == "set" && a2 != "":
		if text.IsInt(a2) && portGE(a2, 443) {
			portUpdate(a2, noRestart)
		} else {
			fmt.Println("Invalid port format. Please provide a valid port like '2083'.")
			portUsage()
			return 1
		}
	case a1 == "default":
		portUpdate("2083", noRestart)
	default:
		portUsage()
		return 1
	}
	return 0
}

// portGE compares a digit string numerically without overflowing
func portGE(s string, n int) bool {
	s = strings.TrimLeft(s, "0")
	if len(s) > 9 {
		return true
	}
	return text.Atoi(s) >= n
}

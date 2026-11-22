// ##############################################################################
// Script Name: internal/commands/user/varnish.go
// Description: Enable/disable Varnish Caching for user and display current status.
// Usage: opencli user-varnish <USERNAME> [enable|disable|status]
// Author: Stefan Pejcic
// Created: 20.03.2025
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
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-varnish",
		Desc:  "Enable/disable Varnish Caching for user and display current status.",
		Usage: "opencli user-varnish <USERNAME> [enable|disable|status]",
		Run:   varnish,
	})
}

var varnishWS = regexp.MustCompile(`nginx|openresty|apache|openlitespeed|litespeed`)

type varnishCtx struct {
	ctx string
}

func (v *varnishCtx) toggle(args ...string) {
	podman.ComposeUser(v.ctx, args...).Dir("/home/" + v.ctx).NoErr().Run()
}

func (v *varnishCtx) check(action string) {
	running := false
	if out, _ := podman.ComposeUser(v.ctx, "ps", "-q", "varnish").Dir("/home/" + v.ctx).Out(); out != "" {
		running = true
	}
	switch action {
	case "start":
		if running {
			fmt.Println("Varnish Cache is now enabled.")
		} else {
			fmt.Println("Failed to enable Varnish Cache.")
		}
	case "stop":
		if !running {
			fmt.Println("Varnish Cache is now disabled.")
		} else {
			fmt.Println("Failed to disable Varnish Cache.")
		}
	case "status":
		if running {
			fmt.Println("Varnish Cache is enabled.")
		} else {
			fmt.Println("Varnish Cache is disabled.")
		}
	}
}

// sedRange is sed -i "/container_name: WS$/,/HTTPS_PORT/ s|from|to|" on the compose file
func varnishSedRange(file, ws, from, to string) {
	b, err := os.ReadFile(file)
	if err != nil {
		cli.Errf("sed: can't read %s: No such file or directory", file)
		return
	}
	start := regexp.MustCompile("container_name: " + ws + "$")
	end := regexp.MustCompile("HTTPS_PORT")
	lines := strings.Split(string(b), "\n")
	in := false
	for i, l := range lines {
		if !in {
			if start.MatchString(l) {
				in = true
			} else {
				continue
			}
		} else if end.MatchString(l) {
			in = false
		}
		lines[i] = strings.Replace(l, from, to, 1)
	}
	conf.WriteFileKeep(file, strings.Join(lines, "\n"), false)
}

func varnishEnvSwap(file, from, to string) {
	b, err := os.ReadFile(file)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, from) {
			lines[i] = to + l[len(from):]
		}
	}
	conf.WriteFileKeep(file, strings.Join(lines, "\n"), false)
}

func varnish(args []string) int {
	username := cli.Arg(args, 0)
	action := cli.Arg(args, 1)
	if username == "" {
		cli.Fail("Usage: opencli user-varnish <user> [enable|disable]")
	}
	v := &varnishCtx{ctx: username}
	envFile := "/home/" + username + "/.env"
	composeFile := podman.ComposeFile(username)
	if !conf.IsFile(envFile) {
		cli.Fail("Error: %s not found!", envFile)
	}

	if action == "" {
		status := "Unknown"
		hasOff, hasOn := false, false
		for _, l := range conf.Lines(envFile) {
			if strings.HasPrefix(l, "#PROXY_HTTP_PORT=") {
				hasOff = true
			}
			if strings.HasPrefix(l, "PROXY_HTTP_PORT=") {
				hasOn = true
			}
		}
		if hasOff {
			status = "Off"
		} else if hasOn {
			status = "On"
		}
		fmt.Println("Current status: " + status)
		return 0
	}

	ws := ""
	for _, l := range conf.Lines(envFile) {
		if !strings.HasPrefix(l, "WEB_SERVER=") {
			continue
		}
		f := strings.SplitN(l, "=", 3)[1]
		f = strings.Join(strings.Fields(f), "")
		if len(f) >= 2 && f[0] == '"' && f[len(f)-1] == '"' {
			f = f[1 : len(f)-1]
		}
		if m := varnishWS.FindString(f); m != "" {
			ws = m
			break
		}
	}
	switch action {
	case "enable":
		v.toggle("down", ws)
		varnishEnvSwap(envFile, "#PROXY_HTTP_PORT=", "PROXY_HTTP_PORT=")
		// podman-compose can't resolve nested ${VAR:-${VAR}} defaults, so the compose file's webserver port is kept flat and swapped here instead
		varnishSedRange(composeFile, ws, "${HTTP_PORT}", "${PROXY_HTTP_PORT}")
		v.toggle("up", "-d", ws, "varnish")
		v.check("start")
	case "disable":
		v.toggle("down", ws)
		varnishEnvSwap(envFile, "PROXY_HTTP_PORT=", "#PROXY_HTTP_PORT=")
		varnishSedRange(composeFile, ws, "${PROXY_HTTP_PORT}", "${HTTP_PORT}")
		v.toggle("down", "varnish")
		v.toggle("up", "-d", ws)
		v.check("stop")
	case "status":
		v.check("status")
	default:
		cli.Fail("Invalid action. Use 'status', 'enable' or 'disable'.")
	}
	return 0
}

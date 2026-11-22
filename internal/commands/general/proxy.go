// ##############################################################################
// Script Name: internal/commands/general/proxy.go
// Description: View and change proxy path '/openpanel' for accessing openpanel.
// Usage: opencli proxy [set <path>|default] [--no-restart]
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
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "proxy",
		Desc:  "View and change proxy path '/openpanel' for accessing openpanel.",
		Usage: "opencli proxy [set <path>|default] [--no-restart]",
		Run:   proxy,
	})
}

var (
	proxyPathRe  = regexp.MustCompile(`/([^*]*)\*`)
	proxySubRe   = regexp.MustCompile(`(@openpanel path /)[^*]*\*`)
	proxyValidRe = regexp.MustCompile(`^(/?[a-zA-Z0-9_-]+)$`)
)

func proxyCurrent() string {
	var out []string
	for _, l := range conf.Lines(portRedirects) {
		if strings.HasPrefix(l, "@openpanel path") {
			m := proxyPathRe.FindStringSubmatch(l)
			if m != nil {
				out = append(out, m[1])
			} else {
				out = append(out, "")
			}
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func proxyUsage() {
	fmt.Println("Usage:")
	fmt.Println("")
	fmt.Println("opencli proxy                        - displays current path")
	fmt.Println("opencli proxy set /custompath        - set /custompath for access")
	fmt.Println("opencli proxy default                - set /openpanel for access")
	fmt.Println("")
	fmt.Println("Add --no-restart to skip restarting services after the change.")
}

func proxyUpdate(newPath string, noRestart bool) {
	if proxyCurrent() == newPath {
		fmt.Printf("Path /%s is already set for accessing the user panel.\n", newPath)
		cli.Exit(0)
	}
	sedFile(portRedirects, func(lines []string) []string {
		for i, l := range lines {
			if m := proxySubRe.FindStringSubmatchIndex(l); m != nil {
				lines[i] = l[:m[0]] + l[m[2]:m[3]] + newPath + "*" + l[m[1]:]
			}
		}
		return lines
	})
	// the redirect lives in caddy, a reload applies it without dropping connections
	podman.CaddyReload()
	if !noRestart {
		sh.C("podman-compose", "restart", "openpanel").Dir("/root").Quiet().Background()
	}
	fmt.Printf("/%s is now set for accessing the OpenPanel interface.\n", newPath)
}

func proxy(args []string) int {
	noRestart := cli.HasFlag(args, "--no-restart")
	a1, a2 := cli.Arg(args, 0), cli.Arg(args, 1)
	switch {
	case a1 == "":
		fmt.Println(proxyCurrent())
	case a1 == "set" && a2 != "":
		if !proxyValidRe.MatchString(a2) {
			fmt.Println("Invalid path format. Please provide a path like '/openpanel' or '/hosting'.")
			proxyUsage()
			return 1
		}
		proxyUpdate(strings.TrimPrefix(a2, "/"), noRestart)
	case a1 == "default":
		proxyUpdate("openpanel", noRestart)
	default:
		proxyUsage()
		return 1
	}
	return 0
}

// ##############################################################################
// Script Name: internal/commands/domains/edit.go
// Description: Open a domain's docroot directory, or its webserver vhost config file in nano with --ws.
// Usage: opencli domains-edit <DOMAIN_NAME> [--ws]
// Author: Stefan Pejcic
// Created: 20.08.2024
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

package domains

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-edit",
		Desc:  "Open a domain's docroot directory, or its webserver vhost config file in nano with --ws.",
		Usage: "opencli domains-edit <DOMAIN_NAME> [--ws]",
		Run:   edit,
	})
}

var wsRe = regexp.MustCompile(`nginx|openresty|apache|openlitespeed|litespeed|caddy`)

// awkField is awk '{print $n}' over every line
func awkField(out string, n int) string {
	var res []string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) >= n {
			res = append(res, f[n-1])
		} else {
			res = append(res, "")
		}
	}
	return strings.TrimRight(strings.Join(res, "\n"), "\n")
}

func editWebServer(context string) string {
	var vals []string
	for _, l := range conf.Lines("/home/" + context + "/.env") {
		if !strings.HasPrefix(l, "WEB_SERVER=") {
			continue
		}
		v := ""
		if f := strings.Split(l, "="); len(f) > 1 {
			v = f[1]
		}
		v = strings.Join(strings.Fields(v), "")
		if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
			v = v[1 : len(v)-1]
		}
		vals = append(vals, v)
	}
	return wsRe.FindString(strings.Join(vals, "\n"))
}

func edit(args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: opencli domains-edit <DOMAIN_NAME> [--ws]")
		return 1
	}
	domain := args[0]
	useWS := cli.Arg(args, 1) == "--ws"

	output, _ := sh.Opencli("domains-whoowns", domain, "--context", "--docroot").Out()
	context := awkField(output, 2)
	docroot := awkField(output, 3)

	if useWS {
		final := "/home/" + context + "/docker-data/volumes/" + context + "_webserver_data/_data/" + domain + ".conf"
		// caddy mode has no vhost, the caddy conf is what serves the domain
		ws := editWebServer(context)
		if ws == "caddy" {
			final = "/etc/openpanel/caddy/domains/" + domain + ".conf"
		}
		if !conf.IsFile(final) {
			fmt.Printf("ERROR: VirtualHosts for domain %s does not exist: %s\n", domain, final)
			return 1
		}
		fmt.Println("Opening VirtualHosts file in nano: " + final)
		sh.C("nano", final).StdinReader(os.Stdin).Run()
		fmt.Printf("Reloading %s webserver to apply changes..\n", ws)
		ws = editWebServer(context)
		if !podman.ReloadWebserver(context, ws) {
			return 1
		}
		return 0
	}
	final := "/home/" + context + "/docker-data/volumes/" + context + "_html_data/_data/" + strings.TrimPrefix(docroot, "/var/www/html/")
	if conf.IsDir(final) {
		fmt.Println("Directory: " + final)
		return 0
	}
	fmt.Println("ERROR: Directory does not exist: " + final)
	return 1
}

// ##############################################################################
// Script Name: internal/commands/domains/varnish.go
// Description: Check Varnish status for domain, enable/disable Varnish caching.
// Usage: opencli domains-varnish <DOMAIN-NAME> [on|off] [--short]
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

package domains

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-varnish",
		Desc:  "Check Varnish status for domain, enable/disable Varnish caching.",
		Usage: "opencli domains-varnish <DOMAIN-NAME> [on|off] [--short]",
		Run:   varnish,
	})
}

// sedRange is sed '/pat/,+n s/^/#/' (comment) or s/^#// (uncomment), a match inside a running range doesn't restart it
func sedRange(file, pat string, n int, comment bool) {
	conf.SedFile(file, func(lines []string) []string {
		left := 0
		for i, l := range lines {
			if left == 0 && strings.Contains(l, pat) {
				left = n + 1
			}
			if left > 0 {
				left--
				if comment {
					lines[i] = "#" + l
				} else {
					lines[i] = strings.TrimPrefix(l, "#")
				}
			}
		}
		return lines
	})
}

func varnish(args []string) int {
	domain := cli.Arg(args, 0)
	if domain == "" {
		fmt.Println("Usage: opencli domains-varnish <domain> <on|off> <--short>")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Println()
		fmt.Println("opencli domains-varnish pejcic.rs               - Display Varnish Cache status for domain")
		fmt.Println("opencli domains-varnish pejcic.rs on            - Enable Varnish Cache for domain with verbose output")
		fmt.Println("opencli domains-varnish pejcic.rs off           - Disable Varnish Cache for domain with verbose output")
		fmt.Println("opencli domains-varnish pejcic.rs on --short    - Enable Varnish Cache")
		fmt.Println("opencli domains-varnish pejcic.rs off --short   - Disable Varnish Cache")
		return 1
	}
	short := cli.Arg(args, 2) == "--short"

	confFile := "/etc/openpanel/caddy/domains/" + domain + ".conf"
	if !conf.IsFile(confFile) {
		if short {
			fmt.Println("error")
		} else {
			fmt.Printf("Error: Configuration file for %s not found!\n", domain)
		}
		return 1
	}

	reloadCaddy := func() {
		podman.CaddyReload()
		if !short {
			fmt.Println("Caddy container reloaded.")
		}
	}

	switch cli.Arg(args, 1) {
	case "on":
		sedRange(confFile, "# Handle HTTPS traffic (port 443)", 6, true)
		sedRange(confFile, "# Terminate TLS and pass to Varnish", 3, false)
		owner, context := varnishContext(domain)
		varnishStart(context, short)
		reloadCaddy()
		varnishMuPlugin(domain, owner, context)
	case "off":
		sedRange(confFile, "# Handle HTTPS traffic (port 443)", 6, false)
		sedRange(confFile, "# Terminate TLS and pass to Varnish", 3, true)
		reloadCaddy()
	default:
		status := "off"
		for _, l := range conf.Lines(confFile) {
			if strings.HasPrefix(l, "#") && strings.Contains(l, "reverse_proxy https://127.0.0.1") {
				status = "on"
				break
			}
		}
		if short {
			fmt.Println(status)
		} else {
			fmt.Printf("Varnish is %s for domain %s\n", strings.ToUpper(status), domain)
		}
	}
	return 0
}

func varnishContext(domain string) (owner, context string) {
	out, _ := sh.Opencli("domains-whoowns", domain, "--context").Out()
	return awkField(out, 1), awkField(out, 2)
}

func varnishStart(context string, short bool) {
	// bash tested the exit code, which is 0 even with no match, so varnish was never started
	if id, _ := podman.User(context, "ps", "-q", "-f", "name=varnish").NoErr().Out(); id != "" {
		if !short {
			fmt.Println("The Varnish container is running.")
		}
		return
	}
	if !short {
		fmt.Println("Varnish is not running, starting..")
	}
	if conf.IsDir("/home/" + context + "/") {
		os.Chdir("/home/" + context + "/")
		podman.ComposeUser(context, "up", "-d", "varnish").NoOut().Run()
	}
}

// https://github.com/stefanpejcic/OpenPanel/issues/853
func varnishMuPlugin(domain, owner, context string) {
	wpJSON, _ := sh.Opencli("websites-user", owner, "--type=wordpress", "--domains="+domain, "--json").Out()
	if wpJSON == "" {
		return
	}
	var paths []string
	if v, err := ojson.Parse(wpJSON); err == nil {
		if arr, ok := v.([]any); ok {
			for _, el := range arr {
				o, ok := el.(*ojson.Object)
				if !ok {
					continue
				}
				sites, _ := o.Get("sites")
				sitesArr, ok := sites.([]any)
				if !ok {
					continue
				}
				for _, s := range sarr(sitesArr) {
					if p, ok := s.Get("path"); ok {
						if str, ok := p.(string); ok && str != "" {
							paths = append(paths, str)
						}
					}
				}
			}
		}
	}
	if len(paths) == 0 {
		return
	}
	st, err := os.Stat("/home/" + context)
	if err != nil {
		return
	}
	uid := int(st.Sys().(*syscall.Stat_t).Uid)
	spec := strconv.Itoa(uid) + ":" + strconv.Itoa(uid)

	for _, wpPath := range strings.Fields(strings.Join(paths, "\n")) {
		hostPath := "/home/" + context + "/docker-data/volumes/" + context + "_html_data/_data/" + strings.TrimPrefix(wpPath, "/var/www/html/")
		if !conf.IsDir(wpPath) {
			continue
		}
		muDir := hostPath + "/wp-content/mu-plugins"
		htmlRoot := tenantfs.Volume(context, "html_data")
		if !conf.IsDir(muDir) {
			if tenantfs.Mkdir(htmlRoot, muDir) != nil {
				continue
			}
			tenantfs.Chown(htmlRoot, muDir, spec)
		}
		muFile := muDir + "/opencli-helper.php"
		if !conf.IsFile(muFile) {
			content, err := os.ReadFile("/etc/openpanel/wordpress/mu-plugin.php")
			if err != nil {
				fmt.Fprintln(os.Stderr, "bash: /etc/openpanel/wordpress/mu-plugin.php: No such file or directory")
				continue
			}
			if tenantfs.Write(htmlRoot, muFile, 0o644, content) != nil {
				continue
			}
			os.Lchown(muFile, uid, uid)
		}
	}
}

// sarr keeps only the object items of a jq .sites[]? iteration
func sarr(a []any) []*ojson.Object {
	var res []*ojson.Object
	for _, x := range a {
		if o, ok := x.(*ojson.Object); ok {
			res = append(res, o)
		}
	}
	return res
}

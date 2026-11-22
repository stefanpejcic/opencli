// ##############################################################################
// Script Name: internal/commands/domains/docroot.go
// Description: View and change docroot for a domain.
// Usage: opencli domains-docroot <DOMAIN_NAME> [update <DOCROOT>] [--debug]
// Author: Stefan Pejcic
// Created: 10.02.2025
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
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/phplisten"
	"opencli/internal/podman"
	"opencli/internal/sh"
	"opencli/internal/tenantfs"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-docroot",
		Desc:  "View and change docroot for a domain.",
		Usage: "opencli domains-docroot <DOMAIN_NAME> [update <DOCROOT>] [--debug]",
		Run:   docroot,
	})
}

var (
	docrootValidRe = regexp.MustCompile(`^/var/www/html/[A-Za-z0-9._/-]*$`)
	docrootPhpRe   = regexp.MustCompile(`php-fpm-[0-9.]+`)
	docrootVerRe   = regexp.MustCompile(`[0-9.]+`)
	docrootPathRe  = regexp.MustCompile(`(/var/www/html/[^>;]*)`)
)

func docrootHelp() int {
	fmt.Println("Usage: opencli domains-docroot <domain> [update <docroot>] [--debug]")
	return 1
}

func docroot(rawArgs []string) int {
	db.Require()
	debug := false
	var args []string
	for _, a := range rawArgs {
		if a == "--debug" {
			debug = true
		} else {
			args = append(args, a)
		}
	}
	if len(args) < 1 {
		return docrootHelp()
	}
	domain, action, newDocroot := args[0], cli.Arg(args, 1), cli.Arg(args, 2)

	log := func(s string) {
		if debug {
			fmt.Println(s)
		}
	}
	log("Debug Mode: ON")
	log("Domain: " + domain)
	if action == "" {
		log("Action: None")
	} else {
		log("Action: " + action)
	}
	if newDocroot == "" {
		log("New Docroot: None")
	} else {
		log("New Docroot: " + newDocroot)
	}

	switch {
	case domain != "" && action == "":
		out, _ := db.Plain("SELECT docroot FROM domains WHERE domain_url = '"+domain+"';", "-N", "-B").Out()
		if out == "" {
			fmt.Printf("Docroot not found for domain: %s - does the domain exist? run: opencli domains-whoowns %s\n", domain, domain)
		} else {
			fmt.Println(out)
		}
		return 0
	case domain != "" && action == "update":
		if newDocroot == "" {
			fmt.Println("Error: Missing new_docroot for update action.")
			return docrootHelp()
		}
		return docrootUpdate(domain, newDocroot, log)
	}
	return docrootHelp()
}

func docrootUpdate(domain, newDocroot string, log func(string)) int {
	log("Updating document root for domain: " + domain + " to: " + newDocroot)
	if !docrootValidRe.MatchString(newDocroot) || strings.Contains(newDocroot, "..") {
		fmt.Println("FATAL ERROR: Invalid docroot. It must start with /var/www/html/ and only use letters, numbers, dots, dashes, underscores and slashes.")
		return 1
	}

	whoowns, _ := sh.Opencli("domains-whoowns", domain).Out()
	user := awkSecondField(whoowns, "Owner of '"+domain+"': ")
	if user == "" {
		fmt.Fprintf(os.Stderr, "Failed to determine the owner of the domain '%s'.\n", domain)
		return 1
	}
	log("User " + user + " owns domain " + domain)

	info, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+user+"';", "-s").Out()
	userID := awkField(info, 1)
	context := awkField(info, 2)
	if userID == "" {
		fmt.Printf("FATAL ERROR: user %s does not exist.\n", user)
		return 1
	}

	db.Plain("UPDATE domains SET docroot='" + db.Escape(newDocroot) + "' WHERE domain_url='" + db.Escape(domain) + "';").Run()
	result, _ := db.Plain("SELECT COUNT(*) FROM domains WHERE docroot = '"+db.Escape(newDocroot)+"' AND domain_url = '"+db.Escape(domain)+"';", "-N").Out()

	if result != "1" {
		log("Updating docroot for domain " + domain + " failed! Contact administrator to check if the mariadb database is running.")
		fmt.Println("Failed to change docroot for domain " + domain)
		return 0
	}

	folder := strings.TrimPrefix(newDocroot, "/var/www/html/")
	newPath := "/home/" + context + "/docker-data/volumes/" + context + "_html_data/_data/" + folder
	log("Creating document root directory " + newDocroot)
	htmlRoot := tenantfs.Volume(context, "html_data")
	if tenantfs.Mkdir(htmlRoot, newPath) != nil {
		fmt.Printf("FATAL ERROR: %s is not a real folder inside /var/www/html/\n", newDocroot)
		return 1
	}
	var uid int
	if st, err := os.Stat("/home/" + user); err == nil {
		uid = int(st.Sys().(*syscall.Stat_t).Uid)
		tenantfs.Chown(htmlRoot, newPath, strconv.Itoa(uid)+":"+strconv.Itoa(uid))
	}

	log("Checking webserver configuration")
	ws := editWebServer(context)
	docrootVhostEdit(domain, context, ws, newDocroot)

	fmt.Printf("Docroot updated to: %s for domain: %s\n", newDocroot, domain)
	return 0
}

func docrootVhostEdit(domain, context, ws, newDocroot string) {
	if ws == "caddy" {
		caddyConf := "/etc/openpanel/caddy/domains/" + domain + ".conf"
		php := ""
		if m := docrootPhpRe.FindString(conf.Read(caddyConf)); m != "" {
			php = docrootVerRe.FindString(m)
		}
		if php != "" {
			phplisten.CaddyConfDirect(caddyConf, context, newDocroot, php)
		}
		sh.C("podman", "exec", "caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile").Quiet().Run()
		return
	}
	vhost := "/home/" + context + "/docker-data/volumes/" + context + "_webserver_data/_data/" + domain + ".conf"
	tenantfs.Edit(tenantfs.Volume(context, "webserver_data"), vhost, func(in []byte) ([]byte, error) {
		s := string(in)
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = docrootPathRe.ReplaceAllLiteralString(l, newDocroot)
		}
		return []byte(strings.Join(lines, "\n")), nil
	})
	podman.ReloadWebserver(context, ws)
}

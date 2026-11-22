// ##############################################################################
// Script Name: internal/commands/websites/user.go
// Description: Lists all websites and domains owned by a specific user.
// Usage: opencli websites-user <USERNAME> [--type=] [--domains=] [--json]
// Author: Stefan Pejcic
// Created: 08.07.2024
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

package websites

import (
	"fmt"
	"os"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "websites-user",
		Desc:  "Lists all websites and domains owned by a specific user.",
		Usage: "opencli websites-user <USERNAME> [--type=] [--domains=] [--json]",
		Run:   user,
	})
}

func userUsage() int {
	out, _ := db.Cmd("SELECT DISTINCT LOWER(type) FROM sites;", "-sN").NoErr().Out()
	types := text.Xargs(out)
	fmt.Println("Usage: opencli websites-user <username> [--type=<type>] [--domains=<domain1,domain2,...>] [--json]")
	if types != "" {
		fmt.Println("Available types: " + types)
	} else {
		fmt.Println("Available types: (none found in database)")
	}
	return 1
}

// userQuery returns "" and prints the error when the query fails
func userQuery(q string) string {
	out, err := db.Cmd(q, "-sN").NoErr().Out()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Database query failed")
		return ""
	}
	return out
}

func userDomains(userID, typeFilter, domainsFilter string) string {
	q := "SELECT d.domain_id, d.domain_url, d.php_version\n                 FROM domains d"
	if typeFilter != "" {
		typeFilter = strings.ToLower(typeFilter)
		q += " INNER JOIN sites s ON s.domain_id = d.domain_id WHERE d.user_id = '" + db.Escape(userID) + "' AND s.type = '" + db.Escape(typeFilter) + "'"
	} else {
		q += " WHERE d.user_id = '" + db.Escape(userID) + "'"
	}
	if domainsFilter != "" {
		parts := strings.Split(domainsFilter, ",")
		if parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		cond := ""
		for _, d := range parts {
			if cond != "" {
				cond += " OR "
			}
			cond += " d.domain_url = '" + db.Escape(d) + "'"
		}
		q += " AND ( " + cond + " )"
	}
	if typeFilter != "" {
		q += " GROUP BY d.domain_id"
	}
	return userQuery(q)
}

func userSites(domainID, typeFilter string) string {
	q := "SELECT site_name, type, version, ports, path, d.docroot FROM sites s INNER JOIN domains d ON s.domain_id = d.domain_id WHERE s.domain_id = '" + db.Escape(domainID) + "'"
	if typeFilter != "" {
		q += " AND s.type = '" + db.Escape(strings.ToLower(typeFilter)) + "'"
	}
	return userQuery(q)
}

func user(args []string) int {
	db.Require()
	if len(args) < 1 {
		return userUsage()
	}
	username := args[0]
	format, filterType, filterDomains := "table", "", ""
	for _, a := range args[1:] {
		switch {
		case a == "--json":
			format = "json"
		case strings.HasPrefix(a, "--type="):
			filterType = a[strings.Index(a, "=")+1:]
		case strings.HasPrefix(a, "--domains="):
			filterDomains = a[strings.Index(a, "=")+1:]
		default:
			return userUsage()
		}
	}

	userID := userQuery("SELECT id FROM users WHERE username = '" + db.Escape(username) + "'")
	if userID == "" {
		// the exit only left the $(...) subshell, so the run goes on with an empty id
		fmt.Fprintf(os.Stderr, "Error: User '%s' not found\n", username)
	}

	domains := userDomains(userID, filterType, filterDomains)
	if format == "json" {
		if domains == "" {
			fmt.Println("{}")
			return 0
		}
		out := ""
		for i, line := range strings.Split(domains, "\n") {
			f := readFields(line, 3, " \t\n")
			if i > 0 {
				out += ","
			}
			out += `"` + strings.ReplaceAll(f[1], "/", `\/`) + `": {"php": "php` + f[2] + `"`
			sites := userSites(f[0], filterType)
			if sites != "" {
				out += `, "sites": [`
				for j, sl := range strings.Split(sites, "\n") {
					s := readFields(sl, 6, " \t\n")
					path := s[4]
					if path == "" {
						path = s[5]
					}
					if j > 0 {
						out += ","
					}
					out += `{"site_name": "` + s[0] + `", "type": "` + s[1] + `", "version": "` + s[2] + `", "ports": "` + s[3] + `", "path": "` + path + `"}`
				}
				out += "]"
			}
			out += "}"
		}
		fmt.Println("{ " + out + " }")
		return 0
	}

	if domains == "" {
		fmt.Printf("No domains found for user '%s'\n", username)
		return 0
	}
	for _, line := range strings.Split(domains, "\n") {
		f := readFields(line, 3, " \t\n")
		fmt.Println("Domain: " + f[1])
		fmt.Println("PHP version: php" + f[2])
		sites := userSites(f[0], filterType)
		if sites != "" {
			fmt.Println("  Sites:")
			for _, sl := range strings.Split(sites, "\n") {
				s := readFields(sl, 6, " \t\n")
				path := s[4]
				if path == "" {
					path = s[5]
				}
				fmt.Println("    Site Name: " + s[0])
				fmt.Println("    Type: " + s[1])
				fmt.Println("    Version: " + s[2])
				fmt.Println("    Ports: " + s[3])
				fmt.Println("    Path: " + path)
				fmt.Println()
			}
		}
		fmt.Println()
	}
	return 0
}

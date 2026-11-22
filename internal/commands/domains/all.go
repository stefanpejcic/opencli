// ##############################################################################
// Script Name: internal/commands/domains/all.go
// Description: Lists all domain names currently hosted on the server.
// Usage: opencli domains-all [--docroot] [--php_version] [--json]
// Author: Stefan Pejcic
// Created: 26.10.2023
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
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/db"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-all",
		Desc:  "Lists all domain names currently hosted on the server.",
		Usage: "opencli domains-all [--docroot] [--php_version] [--json]",
		Run:   all,
	})
}

var digitRuns = regexp.MustCompile(`[0-9]+`)

func jsonQuote(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }

func all(args []string) int {
	db.Require()
	inclDocroot, inclPHP, jsonOut := false, false, false
	for _, a := range args {
		switch a {
		case "--docroot":
			inclDocroot = true
		case "--php_version":
			inclPHP = true
		case "--json":
			jsonOut = true
		default:
			fmt.Println("Unknown option: " + a)
			return 1
		}
	}

	fields := "domain_url"
	if inclDocroot {
		fields += ", docroot"
	}
	if inclPHP {
		fields += ", php_version"
	}
	if dbOut("SELECT "+fields+" FROM domains", 0) == "" {
		fmt.Println("No domains found in the database.")
		return 0
	}

	var query string
	if jsonOut {
		query = "\n            SELECT d.domain_url, d.docroot, d.php_version, u.username, u.server\n            FROM domains d\n            LEFT JOIN users u ON d.user_id = u.id\n        "
	} else {
		query = "SELECT " + fields + " FROM domains"
	}
	domains := dbOut(query, 0)
	if domains == "" {
		fmt.Println("No domains found in the database.")
		return 0
	}
	if !jsonOut {
		fmt.Println(domains)
		return 0
	}

	var b strings.Builder
	b.WriteString(`{"data":{`)
	first := true
	for _, line := range strings.Split(domains, "\n") {
		f := readTabs(line, 5)
		domain, docroot, phpVersion, owner, server := f[0], f[1], f[2], f[3], f[4]
		if owner == "" {
			owner = "null"
		} else {
			owner = jsonQuote(owner)
		}
		prefix := "/var/www/html/"
		if strings.HasPrefix(docroot, prefix) && server != "" {
			docroot = "/home/" + server + "/docker-data/volumes/" + server + "_html_data/_data/" + docroot[len(prefix):]
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		verNum := strings.Join(digitRuns.FindAllString(phpVersion, -1), "")
		verFmt := verNum
		if len(verNum) == 2 {
			verFmt = verNum[:1] + "." + verNum[1:]
		}
		iniPath := ""
		if server != "" && verFmt != "" {
			iniPath = strings.Join(strings.Fields("/home/"+server+"/php.ini/"+verFmt+".ini"), "")
		}
		fmt.Fprintf(&b, `"%s":{`, jsonQuote(domain))
		fmt.Fprintf(&b, `"document_root":"%s",`, jsonQuote(docroot))
		b.WriteString(`"php":{`)
		fmt.Fprintf(&b, `"php_version_id":"%s",`, phpVersion)
		fmt.Fprintf(&b, `"version":"%s",`, verFmt)
		fmt.Fprintf(&b, `"ini_path":"%s",`, iniPath)
		b.WriteString(`"is_native":true,`)
		b.WriteString(`"handler":"php-fpm"`)
		b.WriteString(`},`)
		if owner == "null" {
			b.WriteString(`"owner":null`)
		} else {
			fmt.Fprintf(&b, `"owner":"%s"`, owner)
		}
		b.WriteString("}")
	}
	b.WriteString("}, \"metadata\": {\"result\": \"ok\"}}\n")
	fmt.Print(b.String())
	return 0
}

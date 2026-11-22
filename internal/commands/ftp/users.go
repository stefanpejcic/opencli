// ##############################################################################
// Script Name: internal/commands/ftp/users.go
// Description: Rebuild the combined FTP users list, translating docroot paths to per-user container paths, and cache it to /etc/openpanel/ftp/all.users.
// Usage: /usr/local/opencli/ftp/users.sh (internal helper, not exposed as an opencli subcommand)
// Author: Stefan Pejcic
// Created: 10.09.2024
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

package ftp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"opencli/internal/cli"
)

func init() {
	cli.Register(&cli.Command{
		Name:   "ftp-users",
		Desc:   "Rebuild the combined FTP users list, translating docroot paths to per-user container paths, and cache it to /etc/openpanel/ftp/all.users.",
		Usage:  "/usr/local/opencli/ftp/users.sh (internal helper, not exposed as an opencli subcommand)",
		Hidden: true,
		Run:    users,
	})
}

func users(args []string) int {
	all := ""
	dirs, _ := filepath.Glob(usersDir + "*")
	sort.Strings(dirs)
	for _, d := range dirs {
		b, err := os.ReadFile(d + "/users.list")
		if err != nil {
			continue
		}
		user := filepath.Base(d)
		for _, line := range strings.SplitAfter(string(b), "\n") {
			line = strings.TrimSuffix(line, "\n")
			if line == "" {
				continue
			}
			all += strings.ReplaceAll(line, "/var/www/html/", "/home/"+user+"/docker-data/volumes/"+user+"_html_data/_data/") + " "
		}
	}
	fmt.Println(all)
	os.WriteFile("/etc/openpanel/ftp/all.users", []byte("USERS=\""+all+"\"\n"), 0o644)
	return 0
}

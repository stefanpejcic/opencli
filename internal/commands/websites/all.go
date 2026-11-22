// ##############################################################################
// Script Name: internal/commands/websites/all.go
// Description: Lists all websites currently hosted on the server.
// Usage: opencli websites-all [TYPE]
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

package websites

import (
	"fmt"
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "websites-all",
		Desc:  "Lists all websites currently hosted on the server.",
		Usage: "opencli websites-all [TYPE]",
		Run:   all,
	})
}

func all(args []string) int {
	db.Require()
	siteType := cli.Arg(args, 0)
	query := "SELECT site_name FROM sites"
	if siteType != "" {
		query += " WHERE type = '" + db.Escape(siteType) + "'"
	}
	sites, _ := db.Cmd(query, "-sN").Timeout(10 * time.Second).Out()
	if sites == "" {
		msg := "No sites found"
		if siteType != "" {
			msg += " for type '" + siteType + "'"
		}
		fmt.Println(msg + ".")
	} else {
		fmt.Println(sites)
	}
	return 0
}

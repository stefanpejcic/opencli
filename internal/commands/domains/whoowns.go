// ##############################################################################
// Script Name: internal/commands/domains/whoowns.go
// Description: Check which username owns a certain domain name.
// Usage: opencli domains-whoowns <DOMAIN-NAME> [--context] [--docroot]
// Author: Stefan Pejcic
// Created: 01.10.2023
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
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-whoowns",
		Desc:  "Check which username owns a certain domain name.",
		Usage: "opencli domains-whoowns <DOMAIN-NAME> [--context] [--docroot]",
		Run:   whoowns,
	})
}

func whoowns(args []string) int {
	db.Require()
	if len(args) < 1 || len(args) > 3 {
		fmt.Println("Usage: opencli domains-whoowns <domain_name> [--docroot] [--context]")
		return 1
	}
	docrootFlag, contextFlag := false, false
	domain := args[0]
	for _, a := range args[1:] {
		switch a {
		case "--docroot":
			docrootFlag = true
		case "--context":
			contextFlag = true
		default:
			fmt.Println("Unknown option: " + a)
			fmt.Println("Usage: opencli domains-whoowns <domain_name> [--docroot] [--context]")
			return 1
		}
	}

	f := readFields(dbOut("SELECT user_id, docroot FROM domains WHERE domain_url = '"+db.Escape(domain)+"'", 10*time.Second), 2)
	userID, docroot := f[0], f[1]
	if userID == "" {
		fmt.Printf("Domain '%s' not found in the database.\n", domain)
		return 0
	}
	if contextFlag {
		out, _ := db.Plain("SELECT username, server FROM users WHERE id = '"+userID+"'", "-s").Timeout(10 * time.Second).Out()
		u := readFields(out, 2)
		if u[1] == "" {
			fmt.Printf("Error, no context '%s'.\n", userID)
		} else if docrootFlag {
			fmt.Printf("%s %s %s\n", u[0], u[1], docroot)
		} else {
			fmt.Printf("%s %s\n", u[0], u[1])
		}
		return 0
	}
	username := dbOut("SELECT username FROM users WHERE id = '"+userID+"'", 10*time.Second)
	if username == "" {
		fmt.Printf("User does not exist with that ID '%s'.\n", userID)
	} else if docrootFlag {
		fmt.Printf("Owner of '%s': %s | docroot: %s\n", domain, username, docroot)
	} else {
		fmt.Printf("Owner of '%s': %s\n", domain, username)
	}
	return 0
}

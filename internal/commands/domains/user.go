// ##############################################################################
// Script Name: internal/commands/domains/user.go
// Description: Lists all domain names currently owned by a specific user.
// Usage: opencli domains-user <USERNAME> [--docroot|--php_version]
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

	"opencli/internal/cli"
	"opencli/internal/db"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-user",
		Desc:  "Lists all domain names currently owned by a specific user.",
		Usage: "opencli domains-user <USERNAME> [--docroot|--php_version]",
		Run:   userDomains,
	})
}

func userDomains(args []string) int {
	db.Require()
	if len(args) < 1 {
		fmt.Println("Usage: /usr/local/opencli/domains/user.sh <username> [--docroot] [--php_version]")
		return 1
	}
	username := args[0]
	inclDocroot, inclPHP := false, false
	for _, a := range args[1:] {
		switch a {
		case "--docroot":
			inclDocroot = true
		case "--php_version":
			inclPHP = true
		default:
			fmt.Println("Unknown option: " + a)
			return 1
		}
	}
	userID := dbOut("SELECT id FROM users WHERE username = '"+db.Escape(username)+"'", 0)
	if userID == "" {
		fmt.Printf("User '%s' not found in the database.\n", username)
		return 0
	}
	fields := "domain_url"
	if inclDocroot {
		fields += ", docroot"
	}
	if inclPHP {
		fields += ", php_version"
	}
	domains := dbOut("SELECT "+fields+" FROM domains WHERE user_id = '"+userID+"'", 0)
	if domains == "" {
		fmt.Printf("No domains found for user '%s'.\n", username)
	} else {
		fmt.Println(domains)
	}
	return 0
}

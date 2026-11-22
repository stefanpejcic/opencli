// ##############################################################################
// Script Name: internal/commands/plan/delete.go
// Description: Delete hosting plan
// Usage: opencli plan-delete <PLAN_NAME> [--json]
// Author: Stefan Pejcic
// Created: 01.12.2023
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

package plan

import (
	"fmt"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "plan-delete",
		Desc:  "Delete hosting plan",
		Usage: "opencli plan-delete <PLAN_NAME> [--json]",
		Docs:  "https://docs.openpanel.com",
		Run:   del,
	})
}

func deleteUsage() int {
	fmt.Println("Usage: opencli plan-delete <plan_name> [--json]")
	return 1
}

func del(args []string) int {
	if len(args) < 1 {
		return deleteUsage()
	}
	name := ""
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		} else if name == "" {
			name = a
		} else {
			fmt.Println("Invalid argument: " + a)
			return deleteUsage()
		}
	}

	db.Require()
	esc := db.Escape(name)

	if text.Atoi(db.Value("SELECT COUNT(*) FROM plans WHERE name = '"+esc+"';")) == 0 {
		if asJSON {
			fmt.Printf("{\"error\": \"Plan '%s' not found.\"}\n", name)
		} else {
			fmt.Printf("Plan '%s' not found.\n", name)
		}
		return 1
	}

	where := "FROM users INNER JOIN plans ON users.plan_id = plans.id WHERE plans.name = '" + esc + "' AND username NOT LIKE 'SUSPENDED\\_%';"
	count := text.Atoi(db.Value("SELECT COUNT(*) FROM users INNER JOIN plans ON users.plan_id = plans.id WHERE plans.name = '" + esc + "';"))
	if count > 0 {
		if asJSON {
			data, _ := db.Cmd("SELECT SUBSTRING_INDEX(username, '_', -1) as username "+where, "-B", "-s").Out()
			var items []string
			for _, l := range strings.Split(data, "\n") {
				if l == "" {
					continue
				}
				items = append(items, `"`+strings.ReplaceAll(l, `"`, `\"`)+`"`)
			}
			fmt.Printf("{\"error\": \"Cannot delete plan '%s' as there are users assigned to it.\", \"users\": [%s]}\n", name, strings.Join(items, ","))
		} else {
			fmt.Printf("Cannot delete plan '%s' as there are users assigned to it. List of users:\n", name)
			data, _ := db.Cmd("SELECT SUBSTRING_INDEX(username, '_', -1) as username "+where, "--table").Out()
			if data != "" {
				fmt.Println(data)
			} else {
				fmt.Printf("No users on plan '%s'.\n", name)
			}
		}
		return 1
	}

	db.Exec("DELETE FROM plans WHERE name = '" + esc + "';")
	netGone := sh.C("podman", "network", "rm", name).Quiet().OK()
	if asJSON {
		if netGone {
			fmt.Printf("{\"message\": \"Plan '%s' and Podman network '%s' deleted successfully.\"}\n", name, name)
		} else {
			fmt.Printf("{\"message\": \"Plan '%s' deleted successfully.\"}\n", name)
		}
	} else {
		if netGone {
			fmt.Printf("Podman network '%s' deleted successfully.\n", name)
		}
		fmt.Printf("Plan '%s' deleted successfully.\n", name)
	}
	return 0
}

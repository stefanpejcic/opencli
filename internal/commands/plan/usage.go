// ##############################################################################
// Script Name: internal/commands/plan/usage.go
// Description: Display all users that are currently using the plan.
// Usage: opencli plan-usage <PLAN_NAME> [--json]
// Author: Stefan Pejcic
// Created: 30.11.2023
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
	"opencli/internal/ojson"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "plan-usage",
		Desc:  "Display all users that are currently using the plan.",
		Usage: "opencli plan-usage <PLAN_NAME> [--json]",
		Docs:  "https://docs.openpanel.com",
		Run:   usage,
	})
}

func usageHelp() int {
	fmt.Println("Usage: plan-usage <plan_name> [--json]")
	return 1
}

func usage(args []string) int {
	if len(args) < 1 {
		return usageHelp()
	}
	name := args[0]
	asJSON := false
	for _, a := range args[1:] {
		if a != "--json" {
			return usageHelp()
		}
		asJSON = true
	}
	db.Require()

	q := "SELECT users.id, users.username, users.email, plans.name AS plan_name, users.registered_date FROM users INNER JOIN plans ON users.plan_id = plans.id WHERE plans.name = '" + db.Escape(name) + "';"
	if asJSON {
		data, _ := db.Cmd(q).Out()
		// drop the header row
		if i := strings.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = ""
		}
		out := []any{}
		for _, l := range strings.Split(data, "\n") {
			if l == "" {
				continue
			}
			f := strings.Split(l, "\t")
			o := ojson.New()
			for i, k := range []string{"id", "username", "email", "plan_name", "registered_date"} {
				if i < len(f) {
					o.Set(k, f[i])
				} else {
					o.Set(k, nil)
				}
			}
			out = append(out, o)
		}
		if len(out) == 0 {
			fmt.Println("[]")
		} else {
			fmt.Println(ojson.Pretty(out))
		}
		return 0
	}
	data, _ := db.Cmd(q, "--table").Out()
	if data != "" {
		fmt.Println(data)
	} else {
		fmt.Printf("No users on plan '%s'.\n", name)
	}
	return 0
}

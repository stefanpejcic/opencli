// ##############################################################################
// Script Name: internal/commands/plan/list.go
// Description: Display all plans: id, name, description, limits..
// Usage: opencli plan-list [--json]
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
	"opencli/internal/requirement"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "plan-list",
		Desc:  "Display all plans: id, name, description, limits..",
		Usage: "opencli plan-list [--json]",
		Docs:  "https://docs.openpanel.com",
		Run:   list,
	})
}

func list(args []string) int {
	jsonOut := false
	for _, a := range args {
		if a != "--json" {
			fmt.Println("Usage: opencli plan-list [--json]")
			return 1
		}
		jsonOut = true
	}
	db.Require()
	if jsonOut {
		listJSON()
	} else {
		listTable()
	}
	return 0
}

func listJSON() {
	requirement.Command("jq")
	data, _ := db.Cmd("SELECT * FROM plans;").Out()
	if len(strings.Split(data, "\n")) < 2 {
		fmt.Println("No plans.")
		return
	}
	// keys come from the header row, so columns added later (upsell_plan_id, upsell_url) show up too
	rows := db.Split(data)
	cols := rows[0]
	out := []any{}
	for _, r := range rows[1:] {
		o := ojson.New()
		for i, c := range cols {
			var v any
			if i < len(r) {
				v = r[i]
				if r[i] == "NULL" {
					v = nil
				}
			}
			o.Set(c, v)
		}
		out = append(out, o)
	}
	fmt.Println(ojson.Pretty(out))
}

func listTable() {
	data, _ := db.Cmd("SELECT * FROM plans;", "--table").Out()
	if data != "" {
		fmt.Println(data)
	} else {
		fmt.Println("No plans.")
	}
}

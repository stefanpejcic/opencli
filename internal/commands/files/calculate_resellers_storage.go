// ##############################################################################
// Script Name: internal/commands/files/calculate_resellers_storage.go
// Description: Calculates total disk usage for all resellers.
// Usage: opencli files-calculate_resellers_storage
// Author: Stefan Pejcic
// Created: 24.09.2025
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

package files

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/reseller"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "files-calculate_resellers_storage",
		Desc:  "Calculates total disk usage for all resellers.",
		Usage: "opencli files-calculate_resellers_storage",
		Run:   calcResellersStorage,
	})
}

func calcResellersStorage(args []string) int {
	db.Require()
	resellers := ""
	out, _ := sh.Opencli("admin", "list").Out()
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "|")
		if len(f) >= 3 && f[1] == "reseller" && f[2] == "1" {
			names = append(names, f[0])
		}
	}
	resellers = strings.Join(names, "\n")
	fmt.Println("Total resellers: " + strconv.Itoa(len(strings.Fields(resellers))))
	fmt.Println()

	quotaJSON := sh.Opencli("user-quota").NoErr().Str()

	for _, rs := range strings.Fields(resellers) {
		users, _ := db.Cmd("SELECT username FROM users WHERE owner='"+db.Escape(rs)+"';", "-N").Out()
		if users == "" {
			fmt.Println(rs + " has no user accounts - skipping..")
			continue
		}
		fmt.Printf("%s has (%d) accounts - total disk blocks usage:\n", rs, len(strings.Fields(users)))

		var totalUsed int64
		file := reseller.Dir + "/" + rs + ".json"
		var hard any = json.Number("0")
		var obj *ojson.Object
		var loadErr error
		if st, err := os.Stat(file); err == nil && st.Mode().IsRegular() {
			obj, loadErr = reseller.Load(file)
			if loadErr == nil {
				if v, ok := obj.Get("max_disk_blocks"); ok && v != nil && v != false {
					hard = v
				}
			}
		} else {
			fmt.Println("Warning: Reseller config file not found: " + file)
		}

		used := calcDiskUsed(quotaJSON)
		for _, u := range strings.Fields(users) {
			totalUsed += used[u]
		}

		tmp := "/tmp/" + rs + "_config.json"
		os.WriteFile(tmp, nil, 0o666&^calcUmask())
		switch {
		case obj == nil && loadErr == nil:
			fmt.Fprintf(os.Stderr, "jq: error: Could not open %s: No such file or directory\n", file)
		case loadErr != nil:
			fmt.Fprintf(os.Stderr, "jq: error (at %s:0): %s\n", file, loadErr)
		default:
			obj.Set("current_disk_blocks", json.Number(strconv.FormatInt(totalUsed, 10)))
			obj.Set("max_disk_blocks", hard)
			if os.WriteFile(tmp, []byte(ojson.Pretty(obj)+"\n"), 0o666&^calcUmask()) == nil {
				if os.Rename(tmp, file) != nil {
					sh.Quiet("mv", tmp, file)
				}
			}
		}

		fmt.Printf("%d / %s (blocks)\n", totalUsed, ojson.Text(hard))
		fmt.Println()
		fmt.Println("------------------------------------------------")
	}
	return 0
}

func calcUmask() os.FileMode {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return os.FileMode(m)
}

// calcDiskUsed is jq `.users[] | select(.username==$u) | .disk_used // 0` summed per user
func calcDiskUsed(quota string) map[string]int64 {
	res := map[string]int64{}
	v, err := ojson.Parse(quota)
	if err != nil {
		return res
	}
	top, ok := v.(*ojson.Object)
	if !ok {
		return res
	}
	arr, _ := top.Get("users")
	list, ok := arr.([]any)
	if !ok {
		return res
	}
	for _, it := range list {
		o, ok := it.(*ojson.Object)
		if !ok {
			continue
		}
		name := o.Str("username")
		d, _ := o.Get("disk_used")
		var n int64
		switch x := d.(type) {
		case json.Number:
			n, _ = strconv.ParseInt(x.String(), 10, 64)
		}
		res[name] += n
	}
	return res
}

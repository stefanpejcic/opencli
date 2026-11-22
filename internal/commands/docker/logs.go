// ##############################################################################
// Script Name: internal/commands/docker/logs.go
// Description: Display log sizes for user and system containers
// Usage: opencli docker-logs [--all|--system|--users|<USERNAME>]
// Author: Stefan Pejcic
// Created: 28.05.2025
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

package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/ojson"
	"opencli/internal/podman"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "docker-logs",
		Desc:  "Display log sizes for user and system containers",
		Usage: "opencli docker-logs [--all|--system|--users|<USERNAME>]",
		Run:   logs,
	})
}

func logsUsage() int {
	fmt.Print(`Usage: opencli docker-logs [options]

Options:
  <USERNAME>                                    Display log sizes for specified user.
  --system                                      Display log sizes just for system containers.
  --users                                       Display log sizes just for user containers.
  --all                                         Display log sizes for all user and system containers.

Examples:
  opencli docker-logs stefan
  opencli docker-logs --users
  opencli docker-logs --system
  opencli docker-logs --all
`)
	return 1
}

type logRow struct {
	size int64
	line string
}

func printLogs(context string) {
	var dir string
	if context == "" || context == "default" {
		dir = "/var/lib/containers/storage/overlay-containers"
		fmt.Println("System Containers")
	} else {
		dir = "/home/" + context + "/docker-data/overlay-containers"
		fmt.Println("Context: " + context)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		fmt.Printf("- Log directory not found for context '%s' (%s)\n", context, dir)
		return
	}
	fmt.Println("Container Name | Log Size")
	fmt.Println("----------------------------------------------------")

	ctx := context
	if ctx == "" {
		ctx = "default"
	}
	files, _ := filepath.Glob(dir + "/*/*-json.log")
	var rows []logRow
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		id := filepath.Base(filepath.Dir(f))
		name := podman.Ctx(ctx, "inspect", "--format={{.Name}}", id).NoErr().Str()
		name = strings.TrimPrefix(name, "/")
		if name == "" {
			continue
		}
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		human, _, _ := strings.Cut(sh.Str("du", "-h", f), "\t")
		rows = append(rows, logRow{st.Size(), strconv.FormatInt(st.Size(), 10) + " " + name + " | " + human})
	}
	// sort -nr, equal sizes fall back to the whole line reversed
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].size != rows[j].size {
			return rows[i].size > rows[j].size
		}
		return rows[i].line > rows[j].line
	})
	for _, r := range rows {
		_, rest, _ := strings.Cut(r.line, " ")
		fmt.Println(rest)
	}
	fmt.Println("")
}

// userContexts is the context of every non suspended user
func userContexts(cmd *sh.Cmd) []string {
	out, _ := cmd.Out()
	v, err := ojson.Parse(out)
	if err != nil {
		return nil
	}
	o, ok := v.(*ojson.Object)
	if !ok {
		return nil
	}
	data, _ := o.Get("data")
	arr, _ := data.([]any)
	var res []string
	for _, it := range arr {
		u, ok := it.(*ojson.Object)
		if !ok || strings.HasPrefix(u.Str("username"), "SUSPENDED_") {
			continue
		}
		var c any
		if x, ok := u.Get("context"); ok {
			c = x
		}
		res = append(res, strings.Fields(ojson.Text(c))...)
	}
	return res
}

func logs(args []string) int {
	switch a := cli.Arg(args, 0); a {
	case "--all":
		printLogs("default")
		for _, c := range userContexts(userListCmd()) {
			printLogs(c)
		}
	case "--users":
		for _, c := range userContexts(userListCmd()) {
			printLogs(c)
		}
	case "--system":
		printLogs("default")
	case "":
		return logsUsage()
	default:
		printLogs(a)
	}
	return 0
}

func userListCmd() *sh.Cmd { return sh.Opencli("user-list", "--local", "--json").NoErr() }

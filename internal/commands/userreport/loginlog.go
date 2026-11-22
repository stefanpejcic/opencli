// ##############################################################################
// Script Name: internal/commands/userreport/loginlog.go
// Description: View user's .lastlogin file with last 20 successfull logins.
// Usage: opencli user-loginlog <USERNAME> [--table|--text|--json]
// Author: Stefan Pejcic
// Created: 16.11.2023
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

package userreport

import (
	"fmt"
	"os"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-loginlog",
		Desc:  "View user's .lastlogin file with last 20 successfull logins.",
		Usage: "opencli user-loginlog <USERNAME> [--table|--text|--json]",
		Run:   loginlog,
	})
}

func loginlogUsage() {
	fmt.Println("Usage: opencli user-loginlog <username> [--json | --table]")
	cli.Exit(1)
}

// awk split(s, a, sep) and a[n], "" when missing
func awkPart(s, sep string, n int) string {
	p := strings.Split(s, sep)
	if n-1 < len(p) {
		return p[n-1]
	}
	return ""
}

func loginlogFields(line string) (string, string, string) {
	return awkPart(awkPart(line, " - ", 1), ": ", 2), awkPart(awkPart(line, " - ", 2), ": ", 2), awkPart(awkPart(line, " - ", 3), ": ", 2)
}

// awk records, the last line counts even without a newline
func awkLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func loginlog(args []string) int {
	jsonOut, tableOut, textOut := false, true, false
	username := ""
	for _, a := range args {
		switch {
		case a == "--json":
			tableOut, jsonOut = false, true
		case a == "--table":
			tableOut = true
		case a == "--text":
			tableOut, textOut = false, true
		case strings.HasPrefix(a, "-"):
			loginlogUsage()
		default:
			username = a
		}
	}
	if username == "" {
		loginlogUsage()
	}

	file := conf.UsersDir + "/" + username + "/.lastlogin"
	if !conf.IsFile(file) {
		fmt.Println("Login log file not found for user: " + username)
		return 1
	}
	content := conf.Read(file)

	switch {
	case tableOut: // table (default since 1.7.41)
		var b strings.Builder
		b.WriteString("IP\tCountry\tTime\n")
		for _, l := range awkLines(content) {
			ip, country, t := loginlogFields(l)
			fmt.Fprintf(&b, "%s\t%s\t%s\n", ip, country, t)
		}
		return sh.C("column", "-t", "-s", "\t").Stdin(b.String()).Code()
	case jsonOut:
		// built as text first like the awk did, so odd characters break it the same way
		var b strings.Builder
		b.WriteString("[\n")
		for i, l := range awkLines(content) {
			if i > 0 {
				b.WriteString(",\n")
			}
			ip, country, t := loginlogFields(l)
			b.WriteString("{\n\t\"ip\": \"" + ip + "\",\n\t\"country\": \"" + country + "\",\n\t\"time\": \"" + t + "\"\n}\n")
		}
		b.WriteString("\n]")
		data, _ := echoE(b.String())
		v, err := ojson.Parse(data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jq: parse error: "+err.Error())
			return 5
		}
		fmt.Println(ojson.Pretty(v))
	case textOut:
		fmt.Print(content)
		fmt.Println()
	}
	return 0
}

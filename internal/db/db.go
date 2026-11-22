// ##############################################################################
// Script Name: internal/db/db.go
// Description: MariaDB access through the mariadb client, same flags and output the scripts used.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 15.11.2023
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

package db

import (
	"fmt"
	"os"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/sh"
)

// ConfigFile and Database replace db.sh's config_file and mysql_database
const (
	ConfigFile = "/etc/my.cnf"
	Database   = "panel"
)

// Require is what sourcing db.sh did, it exits when the client config is missing
func Require() {
	if _, err := os.Stat(ConfigFile); err != nil {
		fmt.Println("FATAL ERROR: MariaDB config file not available: " + ConfigFile)
		cli.Exit(1)
	}
}

// Escape makes a value safe inside a single quoted sql literal, same as mysql_escape
func Escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}

// Cmd builds `mariadb --defaults-extra-file=/etc/my.cnf -D panel <flags...> -e <sql>`
func Cmd(sql string, flags ...string) *sh.Cmd {
	args := append([]string{"--defaults-extra-file=" + ConfigFile, "-D", Database}, flags...)
	return sh.C("mariadb", append(args, "-e", sql)...)
}

// Query returns rows without headers, tab separated (-N -B), stderr goes to the terminal
func Query(sql string) (string, error) { return Cmd(sql, "-N", "-B").Out() }

// Value returns the first column of the first row, "" when nothing matched or on error
func Value(sql string) string {
	out, _ := Cmd(sql, "-N", "-B").NoErr().Out()
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	if i := strings.IndexByte(out, '\t'); i >= 0 {
		out = out[:i]
	}
	return out
}

// Rows splits -N -B output into rows of columns, errors drop to an empty result
func Rows(sql string) [][]string {
	out, err := Cmd(sql, "-N", "-B").NoErr().Out()
	if err != nil {
		return nil
	}
	return Split(out)
}

// Split turns tab separated mariadb output into rows
func Split(out string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows
}

// Exec runs a statement and returns its error, output goes to the terminal
func Exec(sql string) error { return Cmd(sql).Run() }

// ExecTimeout runs a statement with a timeout and no output, like `timeout N mariadb ... -e`
func ExecTimeout(sql string, d time.Duration) error { return Cmd(sql).Timeout(d).Quiet().Run() }

// Plain builds `mariadb <flags...> -e <sql>` without the defaults file and database, for scripts that called mariadb -se directly
func Plain(sql string, flags ...string) *sh.Cmd {
	return sh.C("mariadb", append(flags, "-e", sql)...)
}

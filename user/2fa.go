////////////////////////////////////////////////////////////////////////////////
// Script Name: user/2fa.go
// Description: Check or disable 2FA for a user.
// Usage: opencli user-2fa <username> [disable]
// Docs: https://docs.openpanel.com
// Author: Stefan Pejcic
// Created: 16.11.2023
// Last Modified: 25.02.2026
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
////////////////////////////////////////////////////////////////////////////////

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const dbConfigFile = "/usr/local/opencli/db.sh"

// ANSI colour codes
const (
	colorGreen  = "\033[0;32m"
	colorRed    = "\033[0;31m"
	colorYellow = "\033[0;33m"
	colorReset  = "\033[0m"
)

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	if len(args) == 0 || len(args) > 2 {
		fmt.Println("Usage: opencli user-2fa <username> [disable]")
		os.Exit(1)
	}

	username := args[0]
	action := ""
	if len(args) == 2 {
		action = args[1]
	}

	if action == "disable" {
		disable2FA(username)
	} else {
		check2FA(username)
	}
}

// ─────────────────────────────────────────────────────────────
// DB helper
// ─────────────────────────────────────────────────────────────

func mysqlExec(query string) error {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e %q`,
		dbConfigFile, query,
	)
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -se %q`,
		dbConfigFile, query,
	)
	out, err := exec.Command("bash", "-c", script).Output()
	return strings.TrimSpace(string(out)), err
}

// ─────────────────────────────────────────────────────────────
// Actions
// ─────────────────────────────────────────────────────────────

func disable2FA(username string) {
	query := fmt.Sprintf("UPDATE users SET twofa_enabled='0' WHERE username='%s';", username)
	if err := mysqlExec(query); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to disable 2FA for %s: %v\n", username, err)
		os.Exit(1)
	}
	fmt.Printf("Two-factor authentication for %s is now %sDISABLED%s.\n", username, colorRed, colorReset)
}

func check2FA(username string) {
	query := fmt.Sprintf("SELECT twofa_enabled FROM users WHERE username='%s';", username)
	val, err := mysqlQuery(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to query 2FA status for %s: %v\n", username, err)
		os.Exit(1)
	}

	switch val {
	case "0":
		fmt.Printf("Two-factor authentication for %s is %sDISABLED%s.\n", username, colorRed, colorReset)
	case "1":
		fmt.Printf("Two-factor authentication for %s is %sENABLED%s.\n", username, colorGreen, colorReset)
	default:
		fmt.Printf("%sInvalid twofa value for %s.%s\n", colorRed, username, colorReset)
	}
}

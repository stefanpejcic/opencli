////////////////////////////////////////////////////////////////////////////////
// Script Name: user/list.go
// Description: Display all users: id, username, email, plan, registered date.
// Usage: opencli user-list [--json] [--total] [--quota] [--over_quota]
// Docs: https://docs.openpanel.com
// Author: Stefan Pejcic
// Created: 01.10.2023
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
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const (
	dbConfigFile  = "/usr/local/opencli/db.sh"
	repquotaFile  = "/etc/openpanel/openpanel/core/users/repquota"
	repquotaDir   = "/etc/openpanel/openpanel/core/users"
)

// ─────────────────────────────────────────────────────────────
// JSON output structs
// ─────────────────────────────────────────────────────────────

type UserPackage struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

type UserEntry struct {
	ID         interface{} `json:"id"` // int or null
	Username   string      `json:"username"`
	Context    string      `json:"context"`
	Owner      string      `json:"owner"`
	Package    UserPackage `json:"package"`
	Email      string      `json:"email"`
	LocaleCode string      `json:"locale_code"`
}

type UserListResponse struct {
	Data     []UserEntry            `json:"data"`
	Metadata map[string]interface{} `json:"metadata"`
}

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	jsonMode := false
	totalMode := false

	for _, arg := range args {
		switch arg {
		case "--over_quota":
			reportOverQuota()
			os.Exit(0)
		case "--quota":
			reportAllQuotas()
			os.Exit(0)
		case "--json":
			jsonMode = true
		case "--total":
			totalMode = true
		default:
			printUsage()
		}
	}

	if totalMode {
		count := queryTotalUsers()
		if jsonMode {
			fmt.Println(count)
		} else {
			fmt.Printf("Total number of users: %s\n", count)
		}
		os.Exit(0)
	}

	if jsonMode {
		ensureJQInstalled()
		printJSONUsers()
	} else {
		printTableUsers()
	}
}

// ─────────────────────────────────────────────────────────────
// Usage
// ─────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Println("Usage: opencli user-list [--json] [--total]")
	os.Exit(1)
}

// ─────────────────────────────────────────────────────────────
// Quota reports
// ─────────────────────────────────────────────────────────────

func ensureRepquota() {
	if !fileExists(repquotaFile) {
		os.MkdirAll(repquotaDir, 0755)
		exec.Command("quotacheck", "-avm").Run()
		out, _ := exec.Command("repquota", "-u", "/").Output()
		os.WriteFile(repquotaFile, out, 0644)
	}
}

func reportOverQuota() {
	ensureRepquota()
	data, err := os.ReadFile(repquotaFile)
	if err != nil {
		fmt.Println("No users over quota.")
		return
	}
	lines := strings.Split(string(data), "\n")
	hasOver := false
	for _, line := range lines {
		if strings.Contains(line, "+") {
			hasOver = true
			break
		}
	}
	if !hasOver {
		fmt.Println("No users over quota.")
		return
	}
	// Print header lines 3-5 (0-indexed: 2,3,4)
	for i, line := range lines {
		if i >= 2 && i <= 4 {
			fmt.Println(line)
		}
	}
	// Print all lines with '+'
	for _, line := range lines {
		if strings.Contains(line, "+") {
			fmt.Println(line)
		}
	}
}

func reportAllQuotas() {
	ensureRepquota()
	data, err := os.ReadFile(repquotaFile)
	if err != nil {
		fmt.Println("No users quota.")
		return
	}
	lines := strings.Split(string(data), "\n")
	hasRoot := false
	for _, line := range lines {
		if strings.Contains(line, "root") {
			hasRoot = true
			break
		}
	}
	if !hasRoot {
		fmt.Println("No users quota.")
		return
	}
	// tail -n +3 equivalent: skip first 2 lines
	for i, line := range lines {
		if i >= 2 {
			fmt.Println(line)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// DB helpers
// ─────────────────────────────────────────────────────────────

func mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e %q`,
		dbConfigFile, query,
	)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mysqlQueryRaw(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -se %q`,
		dbConfigFile, query,
	)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mysqlQueryTable(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" --table -e %q`,
		dbConfigFile, query,
	)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func queryTotalUsers() string {
	out, err := mysqlQueryRaw("SELECT COUNT(*) FROM users")
	if err != nil {
		return "0"
	}
	return strings.TrimSpace(out)
}

// ─────────────────────────────────────────────────────────────
// Table output (plain)
// ─────────────────────────────────────────────────────────────

func printTableUsers() {
	out, err := mysqlQueryTable(`
		SELECT users.id, users.username, users.email, plans.name AS plan_name,
		       users.server, users.owner, users.registered_date
		FROM users
		INNER JOIN plans ON users.plan_id = plans.id;
	`)
	if err != nil || strings.TrimSpace(out) == "" {
		fmt.Println("No users.")
		return
	}
	fmt.Println(out)
}

// ─────────────────────────────────────────────────────────────
// JSON output
// ─────────────────────────────────────────────────────────────

func printJSONUsers() {
	query := `
		SELECT
		    users.username,
		    users.server,
		    IF(users.owner IS NULL OR users.owner = '', 'root', users.owner) AS owner,
		    plans.name AS package_name,
		    IF(users.owner IS NULL OR users.owner = '', 'root', users.owner) AS package_owner,
		    users.email,
		    'EN_us' AS locale_code
		FROM users
		INNER JOIN plans ON users.plan_id = plans.id;
	`

	out, err := mysqlQueryRaw(query)
	if err != nil || strings.TrimSpace(out) == "" {
		// Emit empty valid response
		resp := UserListResponse{
			Data:     []UserEntry{},
			Metadata: map[string]interface{}{"result": "ok"},
		}
		printJSON(resp)
		return
	}

	var entries []UserEntry

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}

		username    := fields[0]
		server      := fields[1]
		owner       := fields[2]
		packageName := fields[3]
		packageOwner:= fields[4]
		email       := fields[5]
		localeCode  := fields[6]

		// Resolve UID for the server (linux user)
		var uid interface{} = nil
		uidOut, err := exec.Command("id", "-u", server).Output()
		if err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(uidOut))); err == nil {
				uid = n
			}
		}

		entries = append(entries, UserEntry{
			ID:         uid,
			Username:   username,
			Context:    server,
			Owner:      owner,
			Package:    UserPackage{Name: packageName, Owner: packageOwner},
			Email:      email,
			LocaleCode: localeCode,
		})
	}

	if entries == nil {
		entries = []UserEntry{}
	}

	resp := UserListResponse{
		Data:     entries,
		Metadata: map[string]interface{}{"result": "ok"},
	}
	printJSON(resp)
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "    ")
	enc.Encode(v)
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ensureJQInstalled() {
	if _, err := exec.LookPath("jq"); err == nil {
		return
	}
	if _, err := exec.LookPath("apt-get"); err == nil {
		exec.Command("apt-get", "update").Run()
		exec.Command("apt-get", "install", "-y", "-qq", "jq").Run()
	} else if _, err := exec.LookPath("yum"); err == nil {
		exec.Command("yum", "install", "-y", "-q", "jq").Run()
	} else if _, err := exec.LookPath("dnf"); err == nil {
		exec.Command("dnf", "install", "-y", "-q", "jq").Run()
	} else {
		fmt.Println("Error: No compatible package manager found. Please install jq manually and try again.")
		os.Exit(1)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		fmt.Println("Error: jq installation failed. Please install jq manually and try again.")
		os.Exit(1)
	}
}

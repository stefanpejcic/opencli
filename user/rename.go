////////////////////////////////////////////////////////////////////////////////
// Script Name: user/rename.go
// Description: Rename username.
// Usage: opencli user-rename <old_username> <new_username>
// Docs: https://docs.openpanel.com
// Author: Radovan Jecmenica
// Created: 23.11.2023
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
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const (
	dbConfigFile           = "/usr/local/opencli/db.sh"
	forbiddenUsernamesFile = "/etc/openpanel/openadmin/config/forbidden_usernames.txt"
	repquotaPath           = "/etc/openpanel/openpanel/core/users/repquota"
)

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	if len(args) < 2 || len(args) > 3 {
		fmt.Println("Usage: opencli user-rename <old_username> <new_username>")
		os.Exit(1)
	}

	oldUsername := args[0]
	newUsername := args[1]

	// Parse optional flags
	debug := false
	for _, arg := range args[2:] {
		if arg == "--debug" {
			debug = true
		}
	}
	_ = debug // reserved for future use

	// MAIN — mirrors the ordered steps at the bottom of the bash script
	checkUsernameIsValid(newUsername)
	checkIfExistsInDB(newUsername)
	context := getContext(oldUsername)
	mvUserData(oldUsername, newUsername)
	ensureJQInstalled()
	renameUserInDB(oldUsername, newUsername)
	reloadUserQuotas()
	_ = context // context captured, used for docker inspect validation
}

// ─────────────────────────────────────────────────────────────
// Username validation
// ─────────────────────────────────────────────────────────────

func checkUsernameIsValid(newUsername string) {
	if isUsernameInvalid(newUsername) {
		fmt.Printf("Error: The username '%s' is not valid. Ensure it is a single word with no hyphens or underscores, contains only letters and numbers, and has a length between 3 and 20 characters.\n", newUsername)
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		os.Exit(1)
	}
	if isUsernameForbidden(newUsername) {
		fmt.Printf("Error: The username '%s' is not allowed.\n", newUsername)
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#reserved-usernames")
		os.Exit(1)
	}
}

func isUsernameInvalid(username string) bool {
	if strings.ContainsAny(username, " \t-_") {
		return true
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(username) {
		return true
	}
	if len(username) < 3 || len(username) > 20 {
		return true
	}
	return false
}

func isUsernameForbidden(username string) bool {
	f, err := os.Open(forbiddenUsernamesFile)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		forbidden := strings.TrimSpace(scanner.Text())
		if strings.EqualFold(username, forbidden) {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────
// DB helpers
// ─────────────────────────────────────────────────────────────

func mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -sN -e %q`,
		dbConfigFile, query,
	)
	out, err := exec.Command("bash", "-c", script).Output()
	return strings.TrimSpace(string(out)), err
}

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

// ─────────────────────────────────────────────────────────────
// Check new username does not already exist in DB
// ─────────────────────────────────────────────────────────────

func checkIfExistsInDB(newUsername string) {
	// Check username column
	out, err := mysqlQuery(fmt.Sprintf(
		"SELECT COUNT(*) FROM users WHERE username = '%s'", newUsername,
	))
	if err != nil {
		fmt.Println("Error: Unable to check username existence in the database.")
		os.Exit(1)
	}
	if out != "0" && out != "" {
		fmt.Printf("Error: Username '%s' already exists.\n", newUsername)
		os.Exit(1)
	}

	// Check server/context column
	out, err = mysqlQuery(fmt.Sprintf(
		"SELECT COUNT(*) FROM users WHERE server = '%s'", newUsername,
	))
	if err != nil {
		fmt.Println("Error: Unable to check username existence in the database.")
		os.Exit(1)
	}
	if out != "0" && out != "" {
		fmt.Printf("Error: Context '%s' already exists.\n", newUsername)
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────
// Get docker context for old user
// ─────────────────────────────────────────────────────────────

func getContext(oldUsername string) string {
	out, err := mysqlQuery(fmt.Sprintf(
		"SELECT id, server FROM users WHERE username = '%s';", oldUsername,
	))
	if err != nil || strings.TrimSpace(out) == "" {
		fmt.Printf("ERROR: user %s does not exist.\n", oldUsername)
		os.Exit(1)
	}

	fields := strings.Fields(out)
	if len(fields) < 2 {
		fmt.Printf("ERROR: user %s does not exist.\n", oldUsername)
		os.Exit(1)
	}

	context := fields[1]

	// Verify docker context exists
	err = exec.Command("docker", "context", "inspect", context).Run()
	if err != nil {
		fmt.Printf("ERROR: Context '%s' not found.\n", context)
		os.Exit(1)
	}

	return context
}

// ─────────────────────────────────────────────────────────────
// Move user data directories
// ─────────────────────────────────────────────────────────────

func mvUserData(oldUsername, newUsername string) {
	// /etc/openpanel/openpanel/core/users/<username>
	oldCore := fmt.Sprintf("/etc/openpanel/openpanel/core/users/%s", oldUsername)
	newCore := fmt.Sprintf("/etc/openpanel/openpanel/core/users/%s", newUsername)
	os.Rename(oldCore, newCore)

	// /var/log/caddy/stats/<username>/
	oldStats := fmt.Sprintf("/var/log/caddy/stats/%s/", oldUsername)
	newStats := fmt.Sprintf("/var/log/caddy/stats/%s/", newUsername)
	os.Rename(oldStats, newStats)
}

// ─────────────────────────────────────────────────────────────
// Rename user in database
// ─────────────────────────────────────────────────────────────

func renameUserInDB(oldUsername, newUsername string) {
	query := fmt.Sprintf("UPDATE users SET username='%s' WHERE username='%s';", newUsername, oldUsername)
	if err := mysqlExec(query); err != nil {
		fmt.Println("Error: Changing username in database failed!")
		os.Exit(1)
	}
	fmt.Printf("User '%s' successfully renamed to '%s'.\n", oldUsername, newUsername)
}

// ─────────────────────────────────────────────────────────────
// Reload quotas
// ─────────────────────────────────────────────────────────────

func reloadUserQuotas() {
	exec.Command("quotacheck", "-avm").Run()
	out, err := exec.Command("repquota", "-u", "/").Output()
	if err == nil {
		os.MkdirAll("/etc/openpanel/openpanel/core/users/", 0755)
		os.WriteFile(repquotaPath, out, 0644)
	}
}

// ─────────────────────────────────────────────────────────────
// Ensure jq is installed (kept for ecosystem compatibility)
// ─────────────────────────────────────────────────────────────

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

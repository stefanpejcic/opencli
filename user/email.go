////////////////////////////////////////////////////////////////////////////////
// Script Name: user/email.go
// Description: Change email for user
// Usage: opencli user-email <USERNAME> <NEW_EMAIL>
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
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const dbConfigFile = "/usr/local/opencli/db.sh"

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	if len(args) != 2 {
		showUsage()
		os.Exit(1)
	}

	username := args[0]
	newEmail := args[1]

	if err := updateUserEmail(username, newEmail); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		fmt.Fprintf(os.Stderr, "Error: Failed to update email for user '%s'\n", username)
		os.Exit(1)
	}

	fmt.Printf("Success: Email for user '%s' updated to '%s'\n", username, newEmail)
}

// ─────────────────────────────────────────────────────────────
// Usage
// ─────────────────────────────────────────────────────────────

func showUsage() {
	fmt.Println("Usage: opencli user-email <USERNAME> <NEW_EMAIL>")
	fmt.Println()
	fmt.Println("Updates the email address for a specified account.")
	fmt.Println()
	fmt.Println("Arguments:")
	fmt.Println("  USERNAME   - The username of the user to update")
	fmt.Println("  NEW_EMAIL  - The new email address to assign")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  opencli user-email john john.doe@newdomain.com")
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -sN -e %q`,
		dbConfigFile, query,
	)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func validateEmail(email string) error {
	if !emailRegex.MatchString(email) {
		return fmt.Errorf("Error: Invalid email format: %s", email)
	}
	return nil
}

func userExists(username string) (bool, error) {
	out, err := mysqlQuery(fmt.Sprintf(
		"SELECT COUNT(*) FROM users WHERE username = '%s';", username,
	))
	if err != nil {
		return false, fmt.Errorf("Error: Database configuration variables not properly set")
	}
	return strings.TrimSpace(out) == "1", nil
}

func updateUserEmail(username, newEmail string) error {
	// 1. Check user exists
	exists, err := userExists(username)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("Error: User '%s' not found in database", username)
	}

	// 2. Validate email format
	if err := validateEmail(newEmail); err != nil {
		return err
	}

	// 3. Save
	_, err = mysqlQuery(fmt.Sprintf(
		"UPDATE users SET email = '%s' WHERE username = '%s';", newEmail, username,
	))
	if err != nil {
		return fmt.Errorf("Error: Database query failed: %v", err)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////
// Script Name: user/password.go
// Description:  Reset password for a user.
// Usage: opencli user-password <USERNAME> <NEW_PASSWORD | random>
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
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const dbConfigFile = "/usr/local/opencli/db.sh"

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	if len(args) != 2 {
		fmt.Println("Usage: opencli user-password <USERNAME> <NEW_PASSWORD | random>")
		os.Exit(1)
	}

	username := args[0]
	newPassword := args[1]
	randomFlag := false

	// Generate random password if requested
	if newPassword == "random" {
		newPassword = generateRandomPassword()
		randomFlag = true
	}

	// Hash the password using werkzeug via Python
	pythonExec := determinePythonPath()
	hashedPassword := hashPassword(pythonExec, newPassword)

	// Escape single quotes for SQL ('' is the SQL standard escape for ')
	escapedHash := strings.ReplaceAll(hashedPassword, "'", "''")

	// Save to DB and invalidate sessions
	saveToDatabase(username, escapedHash, newPassword, randomFlag)
}

// ─────────────────────────────────────────────────────────────
// Password generation
// ─────────────────────────────────────────────────────────────

const passwordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generateRandomPassword() string {
	b := make([]byte, 12)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(passwordChars))))
		b[i] = passwordChars[idx.Int64()]
	}
	return string(b)
}

// ─────────────────────────────────────────────────────────────
// Python path detection
// ─────────────────────────────────────────────────────────────

func determinePythonPath() string {
	venvPython := "/usr/local/admin/venv/bin/python3"
	if fi, err := os.Stat(venvPython); err == nil && fi.Mode()&0111 != 0 {
		return venvPython
	}
	if path, err := exec.LookPath("python3"); err == nil {
		return path
	}
	fmt.Println("Warning: No Python 3 interpreter found. Please install Python 3 or check the virtual environment.")
	os.Exit(1)
	return ""
}

// ─────────────────────────────────────────────────────────────
// Password hashing (werkzeug)
// ─────────────────────────────────────────────────────────────

func hashPassword(pythonExec, password string) string {
	// Pass password as argv[1] to avoid shell injection — identical to the bash heredoc approach.
	script := `
import sys
from werkzeug.security import generate_password_hash
print(generate_password_hash(sys.argv[1]))
`
	cmd := exec.Command(pythonExec, "-c", script, password)
	out, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error: Failed to hash password: %v\n", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(out))
}

// ─────────────────────────────────────────────────────────────
// Database operations
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

func saveToDatabase(username, escapedHash, plainPassword string, randomFlag bool) {
	// 1. Update password
	updateQuery := fmt.Sprintf("UPDATE users SET password='%s' WHERE username='%s';", escapedHash, username)
	if err := mysqlExec(updateQuery); err != nil {
		fmt.Println("Error: Data insertion failed.")
		os.Exit(1)
	}

	// 2. Invalidate active sessions
	deleteSessionsQuery := fmt.Sprintf(
		"DELETE FROM active_sessions WHERE user_id=(SELECT id FROM users WHERE username='%s');",
		username,
	)
	if err := mysqlExec(deleteSessionsQuery); err != nil {
		fmt.Println("WARNING: Failed to terminate existing sessions for the user.")
	}

	// 3. Success message
	if randomFlag {
		fmt.Printf("Successfully changed password for user %s, new random generated password is: %s\n", username, plainPassword)
	} else {
		fmt.Printf("Successfully changed password for user %s\n", username)
	}
}

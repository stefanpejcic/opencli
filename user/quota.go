////////////////////////////////////////////////////////////////////////////////
// Script Name: user/quota.go
// Description: Enforce and recalculate disk and inodes for a user.
// Usage: opencli user-quota <username|--all>
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
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const (
	dbConfigFile  = "/usr/local/opencli/db.sh"
	repquotaPath  = "/etc/openpanel/openpanel/core/users/repquota"
	gbToBlocks    = 1024000
)

// ─────────────────────────────────────────────────────────────
// Logging
// ─────────────────────────────────────────────────────────────

func timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func log(msg string) {
	fmt.Printf("[%s] %s\n", timestamp(), msg)
}

func logError(msg string) {
	fmt.Fprintf(os.Stderr, "[%s] ERROR: %s\n", timestamp(), msg)
}

// ─────────────────────────────────────────────────────────────
// Usage
// ─────────────────────────────────────────────────────────────

func usage() {
	fmt.Print(`Usage: opencli user-quota <username> OR opencli user-quota --all

Arguments:
    username    Set quota for specific user
    --all       Set quota for all active users

Description:
    This script enforces and recalculates disk and inode quotas for users
    based on their plan limits stored in the database.

Examples:
    opencli user-quota stefan
    opencli user-quota --all
`)
}

// ─────────────────────────────────────────────────────────────
// DB helper
// ─────────────────────────────────────────────────────────────

func mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -N -B -e %q`,
		dbConfigFile, query,
	)
	out, err := exec.Command("bash", "-c", script).Output()
	return strings.TrimSpace(string(out)), err
}

// ─────────────────────────────────────────────────────────────
// Plan limits
// ─────────────────────────────────────────────────────────────

type PlanLimits struct {
	InodeLimit int
	DiskLimitGB int
}

func getPlanLimits(username string) (PlanLimits, error) {
	if username == "" {
		return PlanLimits{}, fmt.Errorf("username parameter is required")
	}

	query := fmt.Sprintf(`SELECT p.inodes_limit, p.disk_limit
FROM users u
JOIN plans p ON u.plan_id = p.id
WHERE u.username = '%s'`, username)

	result, err := mysqlQuery(query)
	if err != nil || strings.TrimSpace(result) == "" {
		return PlanLimits{}, fmt.Errorf("no plan found for user: %s", username)
	}

	fields := strings.Fields(result)
	if len(fields) < 2 {
		return PlanLimits{}, fmt.Errorf("invalid plan limits retrieved for user: %s", username)
	}

	inodes, err := strconv.Atoi(fields[0])
	if err != nil {
		return PlanLimits{}, fmt.Errorf("invalid plan limits retrieved for user: %s", username)
	}

	// Strip " GB" suffix if present
	diskStr := strings.ReplaceAll(fields[1], " GB", "")
	diskStr = strings.TrimSpace(diskStr)
	disk, err := strconv.Atoi(diskStr)
	if err != nil {
		return PlanLimits{}, fmt.Errorf("invalid plan limits retrieved for user: %s", username)
	}

	return PlanLimits{InodeLimit: inodes, DiskLimitGB: disk}, nil
}

// ─────────────────────────────────────────────────────────────
// User existence check
// ─────────────────────────────────────────────────────────────

func validateUserExists(username string) error {
	err := exec.Command("id", username).Run()
	if err != nil {
		return fmt.Errorf("user does not exist: %s", username)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Set quota
// ─────────────────────────────────────────────────────────────

func setUserQuota(username string, limits PlanLimits) error {
	blockLimit := limits.DiskLimitGB * gbToBlocks
	blockStr := strconv.Itoa(blockLimit)
	inodeStr := strconv.Itoa(limits.InodeLimit)
	diskDisplay := strconv.Itoa(limits.DiskLimitGB) + " GB"

	err := exec.Command("sudo", "setquota", "-u", username,
		blockStr, blockStr, inodeStr, inodeStr, "/").Run()
	if err != nil {
		return fmt.Errorf("failed to set quota for user: %s", username)
	}

	log(fmt.Sprintf("Quota set for user %s: %s blocks (%s) and %s inodes",
		username, blockStr, diskDisplay, inodeStr))
	return nil
}

// ─────────────────────────────────────────────────────────────
// Process single user
// ─────────────────────────────────────────────────────────────

func processUser(username string) error {
	log(fmt.Sprintf("Processing user: %s", username))

	limits, err := getPlanLimits(username)
	if err != nil {
		logError(err.Error())
		return err
	}

	if err := validateUserExists(username); err != nil {
		logError(err.Error())
		return err
	}

	if err := setUserQuota(username, limits); err != nil {
		logError(err.Error())
		return err
	}

	return nil
}

// ─────────────────────────────────────────────────────────────
// Process all users
// ─────────────────────────────────────────────────────────────

func getActiveUsers() ([]string, error) {
	out, err := exec.Command("opencli", "user-list", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("no active users found in the database")
	}

	var users []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "SUSPENDED") {
			continue
		}
		if strings.Contains(line, "username") {
			parts := strings.Split(line, `"`)
			if len(parts) >= 4 && parts[3] != "" {
				users = append(users, parts[3])
			}
		}
	}

	if len(users) == 0 {
		return nil, fmt.Errorf("no active users found in the database")
	}
	return users, nil
}

func processAllUsers() error {
	log("Fetching list of active users...")

	users, err := getActiveUsers()
	if err != nil {
		logError(err.Error())
		return err
	}

	total := len(users)
	log(fmt.Sprintf("Found %d active users to process", total))

	var failedUsers []string
	for i, user := range users {
		fmt.Printf("Processing user: %s (%d/%d)\n", user, i+1, total)
		if err := processUser(user); err != nil {
			failedUsers = append(failedUsers, user)
		}
		fmt.Println("------------------------------")
	}

	if len(failedUsers) == 0 {
		log(fmt.Sprintf("Successfully processed all %d users", total))
		return nil
	}

	logError(fmt.Sprintf("Failed to process %d users: %s",
		len(failedUsers), strings.Join(failedUsers, " ")))
	return fmt.Errorf("some users failed")
}

// ─────────────────────────────────────────────────────────────
// Update repquota cache file
// ─────────────────────────────────────────────────────────────

func updateRepquota() error {
	log("Updating repquota file...")
	out, err := exec.Command("repquota", "-u", "/").Output()
	if err != nil {
		logError("Failed to update repquota file")
		return err
	}
	if err := os.WriteFile(repquotaPath, out, 0644); err != nil {
		logError("Failed to update repquota file")
		return err
	}
	log(fmt.Sprintf("Repquota file updated successfully: %s", repquotaPath))
	return nil
}

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]
	exitCode := 0

	if len(args) == 0 || args[0] == "help" {
		usage()
		os.Exit(1)
	}

	if len(args) != 1 {
		logError("Invalid number of arguments")
		usage()
		os.Exit(1)
	}

	switch args[0] {
	case "--all":
		if err := processAllUsers(); err != nil {
			exitCode = 1
		} else {
			log("DONE: All users processed successfully")
		}
	default:
		if err := processUser(args[0]); err != nil {
			exitCode = 1
		}
	}

	if err := updateRepquota(); err != nil {
		exitCode = 1
	}

	os.Exit(exitCode)
}

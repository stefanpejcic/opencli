////////////////////////////////////////////////////////////////////////////////
// Script Name: user/delete.go
// Description: Delete user account and permanently remove all their data.
// Usage: opencli user-delete <username> [-y] [--all]
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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const (
	dbConfigFile = "/usr/local/opencli/db.sh"
)

// ─────────────────────────────────────────────────────────────
// Config / state
// ─────────────────────────────────────────────────────────────

type Config struct {
	ProvidedUsername string
	Username         string
	SkipConfirmation bool
	DeleteAll        bool
	// resolved from DB
	UserID          string
	Context         string // docker context / server field
	ContextFlag     string // "--context <context>"
	NodeIPAddress   string // non-empty when user lives on a remote node
}

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]
	if len(args) < 1 || len(args) > 3 {
		fmt.Println("Usage: opencli user-delete <username> [-y] [--all]")
		os.Exit(1)
	}

	cfg := &Config{}
	cfg.parseFlags(args)

	if cfg.DeleteAll {
		cfg.deleteAllUsers()
	} else {
		if cfg.ProvidedUsername == "" {
			fmt.Println("Error: Username is required unless --all is specified.")
			os.Exit(1)
		}
		cfg.deleteUser(cfg.ProvidedUsername)
	}
}

// ─────────────────────────────────────────────────────────────
// Flag parsing
// ─────────────────────────────────────────────────────────────

func (c *Config) parseFlags(args []string) {
	for _, arg := range args {
		switch arg {
		case "--all":
			c.DeleteAll = true
		case "-y":
			c.SkipConfirmation = true
		default:
			c.ProvidedUsername = arg
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runCmdSilent(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Run()
}

func (c *Config) mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -N -e %q`,
		dbConfigFile, query,
	)
	return runCmd("bash", "-c", script)
}

func (c *Config) mysqlExec(sql string) error {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -e %q`,
		dbConfigFile, sql,
	)
	_, err := runCmd("bash", "-c", script)
	return err
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ─────────────────────────────────────────────────────────────
// Confirmation prompt
// ─────────────────────────────────────────────────────────────

func (c *Config) confirmAction(username string) {
	if c.SkipConfirmation {
		return
	}
	fmt.Printf("This will permanently delete user '%s' and all associated data. Confirm? [Y/n]: ", username)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if response != "" && response != "y" && response != "yes" {
		fmt.Printf("Operation canceled for user '%s'.\n", username)
		os.Exit(0)
	}
}

// ─────────────────────────────────────────────────────────────
// Get user info from DB
// ─────────────────────────────────────────────────────────────

func (c *Config) getUserInfo() {
	query := fmt.Sprintf(`
		SELECT id, server FROM users
		WHERE username='%s'
		UNION ALL
		SELECT id, server FROM users
		WHERE username LIKE 'SUSPENDED_%%%s'
		LIMIT 1;`,
		c.ProvidedUsername, c.ProvidedUsername,
	)

	out, err := c.mysqlQuery(query)
	if err != nil || strings.TrimSpace(out) == "" {
		fmt.Printf("ERROR: User '%s' not found in the database.\n", c.ProvidedUsername)
		os.Exit(1)
	}

	fields := strings.Fields(out)
	if len(fields) < 2 {
		fmt.Printf("ERROR: User '%s' not found in the database.\n", c.ProvidedUsername)
		os.Exit(1)
	}

	c.UserID = fields[0]
	c.Context = fields[1]
	c.ContextFlag = "--context " + c.Context

	// Determine if the user is on a remote node
	if strings.HasPrefix(c.Context, "ssh://") {
		sshHost := strings.TrimPrefix(c.Context, "ssh://")
		parts := strings.SplitN(sshHost, "@", 2)
		if len(parts) == 2 {
			c.NodeIPAddress = parts[1]
		} else {
			c.NodeIPAddress = sshHost
		}
	} else {
		c.NodeIPAddress = ""
	}
}

// ─────────────────────────────────────────────────────────────
// Delete Caddy vhost files
// ─────────────────────────────────────────────────────────────

func (c *Config) deleteVhostFiles() {
	out, _ := runCmd("opencli", "domains-user", c.Username)
	deletedCount := 0
	for _, domain := range strings.Fields(out) {
		confPath := fmt.Sprintf("/etc/openpanel/caddy/domains/%s.conf", domain)
		if err := os.Remove(confPath); err == nil {
			deletedCount++
		}
	}
	// Reload Caddy
	runCmdSilent("docker", "--context", "default", "exec", "caddy",
		"caddy", "reload", "--config", "/etc/caddy/Caddyfile")
}

// ─────────────────────────────────────────────────────────────
// Delete FTP sub-accounts
// ─────────────────────────────────────────────────────────────

func (c *Config) deleteFTPUsers(openpanelUsername string) {
	usersDir := "/etc/openpanel/ftp/users"
	userDir := filepath.Join(usersDir, openpanelUsername)
	usersList := filepath.Join(userDir, "users.list")

	if !dirExists(userDir) {
		return
	}

	if fileExists(usersList) {
		fmt.Println("Checking and removing user's FTP sub-accounts")
		f, err := os.Open(usersList)
		if err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := scanner.Text()
				parts := strings.SplitN(line, "|", 3)
				if len(parts) >= 1 {
					ftpUser := parts[0]
					fmt.Printf("Deleting FTP user: %s\n", ftpUser)
					runCmdSilent("opencli", "ftp-delete", ftpUser, openpanelUsername)
				}
			}
		}
	}

	os.RemoveAll(userDir)
}

// ─────────────────────────────────────────────────────────────
// Delete user from database
// ─────────────────────────────────────────────────────────────

func (c *Config) deleteUserFromDatabase(openpanelUsername string) {
	// 1. Get all domain IDs for this user
	domainIDsOut, _ := c.mysqlQuery(fmt.Sprintf(
		"SELECT domain_id FROM domains WHERE user_id='%s';", c.UserID,
	))

	var sql strings.Builder

	// 2. Delete sites linked to those domains (if any)
	if ids := strings.TrimSpace(domainIDsOut); ids != "" {
		// Convert newline-separated IDs to comma-separated
		idList := strings.Join(strings.Fields(ids), ",")
		sql.WriteString(fmt.Sprintf("DELETE FROM sites WHERE domain_id IN (%s); ", idList))
	}

	// 3. Delete domains
	sql.WriteString(fmt.Sprintf("DELETE FROM domains WHERE user_id='%s'; ", c.UserID))

	// 4. Delete active sessions
	sql.WriteString(fmt.Sprintf("DELETE FROM active_sessions WHERE user_id='%s'; ", c.UserID))

	// 5. Delete the user (including any SUSPENDED_ variant)
	sql.WriteString(fmt.Sprintf(
		"DELETE FROM users WHERE username='%s' OR username LIKE 'SUSPENDED_%%%s';",
		openpanelUsername, openpanelUsername,
	))

	c.mysqlExec(sql.String())
}

// ─────────────────────────────────────────────────────────────
// Delete all user files
// ─────────────────────────────────────────────────────────────

func (c *Config) deleteAllUserFiles() {
	if c.NodeIPAddress != "" {
		remoteScript := fmt.Sprintf(`
pkill -u %s -9 2>/dev/null || true
deluser --remove-home %s 2>/dev/null || true
[ -d /home/%s ] && rm -rf /home/%s
`, c.Context, c.Context, c.Context, c.Context)
		runCmd("ssh", "root@"+c.NodeIPAddress, "bash", "-c", remoteScript)
	}

	runCmdSilent("pkill", "-u", c.Context, "-9")
	runCmdSilent("deluser", "--remove-home", c.Context)

	homeDir := fmt.Sprintf("/home/%s", c.Context)
	if dirExists(homeDir) {
		os.RemoveAll(homeDir)
	}

	coreDir := fmt.Sprintf("/etc/openpanel/openpanel/core/users/%s", c.Context)
	if dirExists(coreDir) {
		os.RemoveAll(coreDir)
	}
}

// ─────────────────────────────────────────────────────────────
// Remove Docker context
// ─────────────────────────────────────────────────────────────

func (c *Config) deleteContext() {
	runCmdSilent("docker", "context", "rm", c.Context)
}

// ─────────────────────────────────────────────────────────────
// Refresh reseller account counts
// ─────────────────────────────────────────────────────────────

func (c *Config) refreshResellersData() {
	resellersDir := "/etc/openpanel/openadmin/resellers"
	if !dirExists(resellersDir) {
		return
	}

	entries, err := os.ReadDir(resellersDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		reseller := strings.TrimSuffix(entry.Name(), ".json")
		jsonFile := filepath.Join(resellersDir, entry.Name())

		out, err := c.mysqlQuery(fmt.Sprintf(
			"SELECT COUNT(*) FROM users WHERE owner='%s';", reseller,
		))
		if err != nil {
			continue
		}
		count := strings.TrimSpace(out)

		runCmd("bash", "-c", fmt.Sprintf(
			`jq '.current_accounts = %s' %q > /tmp/%s_config.json && mv /tmp/%s_config.json %q`,
			count, jsonFile, reseller, reseller, jsonFile,
		))
	}
}

// ─────────────────────────────────────────────────────────────
// Reload quotas
// ─────────────────────────────────────────────────────────────

func (c *Config) reloadUserQuotas() {
	// Touch the file so it exists even if quotacheck is slow
	repquotaPath := "/etc/openpanel/openpanel/core/users/repquota"
	os.MkdirAll(filepath.Dir(repquotaPath), 0755)
	f, err := os.OpenFile(repquotaPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.Close()
	}
	runCmdSilent("quotacheck", "-avm")
	runCmd("bash", "-c", fmt.Sprintf("repquota -u / > %s", repquotaPath))
}

// ─────────────────────────────────────────────────────────────
// Core delete flow for a single user
// ─────────────────────────────────────────────────────────────

func (c *Config) deleteUser(providedUsername string) {
	c.ProvidedUsername = providedUsername

	// Strip SUSPENDED_ prefix to get the display username
	if strings.HasPrefix(providedUsername, "SUSPENDED_") {
		parts := strings.SplitN(providedUsername, "_", 3)
		if len(parts) == 3 {
			c.Username = parts[2]
		} else {
			c.Username = strings.TrimPrefix(providedUsername, "SUSPENDED_")
		}
	} else {
		c.Username = providedUsername
	}

	c.confirmAction(c.Username)
	c.getUserInfo()
	c.deleteVhostFiles()
	c.deleteFTPUsers(providedUsername)
	c.deleteUserFromDatabase(providedUsername)
	c.deleteAllUserFiles()
	c.deleteContext()
	c.refreshResellersData()
	c.reloadUserQuotas()

	fmt.Printf("User %s deleted successfully.\n", c.Username)
}

// ─────────────────────────────────────────────────────────────
// Delete ALL users
// ─────────────────────────────────────────────────────────────

func (c *Config) deleteAllUsers() {
	out, err := runCmd("opencli", "user-list", "--json")
	if err != nil || strings.TrimSpace(out) == "" {
		fmt.Println("No users found in the database.")
		os.Exit(1)
	}

	// Parse usernames from JSON: grep lines with "username", skip SUSPENDED
	var users []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "SUSPENDED") {
			continue
		}
		if strings.Contains(line, "username") {
			parts := strings.Split(line, `"`)
			// format: "username": "value"  → parts[3] is the value
			if len(parts) >= 4 {
				u := parts[3]
				if u != "" {
					users = append(users, u)
				}
			}
		}
	}

	if len(users) == 0 {
		fmt.Println("No users found in the database.")
		os.Exit(1)
	}

	total := len(users)
	for i, user := range users {
		fmt.Printf("- %s (%d/%d)\n", user, i+1, total)
		// Reset per-user state before each deletion
		c.UserID = ""
		c.Context = ""
		c.ContextFlag = ""
		c.NodeIPAddress = ""
		c.deleteUser(user)
		fmt.Println("------------------------------")
	}

	fmt.Println("DONE.")
	fmt.Printf("%d users have been deleted.\n", total)
}

////////////////////////////////////////////////////////////////////////////////
// Script Name: user/change_plan.go
// Description: Change plan for a user and apply new plan limits.
// Usage: opencli user-change_plan <USERNAME> <NEW_PLAN_NAME> [--debug]
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
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const dbConfigFile = "/usr/local/opencli/db.sh"

// ─────────────────────────────────────────────────────────────
// Config / state
// ─────────────────────────────────────────────────────────────

type Config struct {
	ContainerName string
	NewPlanName   string
	Debug         bool

	// current plan (from DB)
	CurrentPlanID   string
	CurrentPlanName string
	Server          string

	// new plan (from DB)
	NewPlanID string

	// new plan limits
	NCPU        int
	NRAM        string // e.g. "2g"
	NRAMNum     int    // numeric GB
	NDiskLimit  int    // GB
	NInodes     int
	NBandwidth  string
	StorageBlocks int // NDiskLimit * 1024000

	// old limits (from compose file, fallback DB)
	OCPU string
	ORAM string

	// server caps
	MaxCPU int
	MaxRAM int

	// counters
	SuccessCount      int
	FailureCount      int
	WriteFailureCount int

	// path
	ComposeFile string
}

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]
	if len(args) < 2 || len(args) > 3 {
		fmt.Println("Usage: opencli user-change-plan <username> <new_plan_name>")
		os.Exit(1)
	}

	cfg := &Config{
		ContainerName: args[0],
		NewPlanName:   args[1],
	}
	for _, a := range args[2:] {
		if a == "--debug" {
			cfg.Debug = true
		}
	}

	cfg.ComposeFile = fmt.Sprintf("/home/%s/docker-compose.yml", cfg.ContainerName)
	if !fileExists(cfg.ComposeFile) {
		fmt.Fprintf(os.Stderr, "Fatal Error: %s does not exist - changing user limits will not be permanent.\n", cfg.ComposeFile)
		os.Exit(1)
	}

	cfg.getCurrentPlanID()
	cfg.getCurrentPlanName()
	cfg.getNewPlanID()

	if cfg.CurrentPlanID == "" {
		fmt.Printf("Error: Container '%s' not found in the database.\n", cfg.ContainerName)
		os.Exit(1)
	}

	// Verify both plans exist
	if !cfg.planLimitsExist(cfg.CurrentPlanID) {
		fmt.Printf("Error: Unable to fetch limits for the current plan ('%s').\n", cfg.CurrentPlanID)
		os.Exit(1)
	}
	if !cfg.planLimitsExist(cfg.NewPlanID) {
		fmt.Printf("Error: Unable to fetch limits for the new plan ('%s').\n", cfg.NewPlanID)
		os.Exit(1)
	}

	cfg.loadNewPlanLimits()
	cfg.loadServerCaps()
	cfg.loadOldLimits()

	// MAIN
	cfg.updateContainerCPU()
	cfg.updateContainerRAM()
	// cfg.updateUserTC() — TODO
	cfg.updateDiskAndInodes()
	cfg.changePlanNameInDB()
	cfg.dropRedisCache()
	cfg.tada()

	os.Exit(0)
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mysqlQuery(query string) (string, error) {
	script := fmt.Sprintf(
		`source %s && mysql --defaults-extra-file="$config_file" -D "$mysql_database" -N -B -e %q`,
		dbConfigFile, query,
	)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runCmdSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// readComposeValue reads KEY=VALUE from the compose file for a given key.
func readComposeValue(composeFile, key string) string {
	data, err := os.ReadFile(composeFile)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

// ─────────────────────────────────────────────────────────────
// DB lookups
// ─────────────────────────────────────────────────────────────

func (c *Config) getCurrentPlanID() {
	out, err := mysqlQuery(fmt.Sprintf(
		"SELECT plan_id, server FROM users WHERE username = '%s'", c.ContainerName,
	))
	if err != nil || out == "" {
		return
	}
	fields := strings.Fields(out)
	if len(fields) >= 1 {
		c.CurrentPlanID = fields[0]
	}
	if len(fields) >= 2 {
		c.Server = fields[1]
	}
	if c.Server == "" || c.Server == "default" {
		c.Server = c.ContainerName
	}
}

func (c *Config) getCurrentPlanName() {
	out, _ := mysqlQuery(fmt.Sprintf(
		"SELECT name FROM plans WHERE id = '%s'", c.CurrentPlanID,
	))
	c.CurrentPlanName = strings.TrimSpace(out)
}

func (c *Config) getNewPlanID() {
	out, _ := mysqlQuery(fmt.Sprintf(
		"SELECT id FROM plans WHERE name = '%s'", c.NewPlanName,
	))
	c.NewPlanID = strings.TrimSpace(out)
}

func (c *Config) planLimitsExist(planID string) bool {
	out, err := mysqlQuery(fmt.Sprintf(
		"SELECT cpu, ram, disk_limit, inodes_limit, bandwidth FROM plans WHERE id = '%s'", planID,
	))
	return err == nil && strings.TrimSpace(out) != ""
}

func (c *Config) getPlanLimit(planID, resource string) string {
	out, _ := mysqlQuery(fmt.Sprintf(
		"SELECT %s FROM plans WHERE id = '%s'", resource, planID,
	))
	return strings.TrimSpace(out)
}

// ─────────────────────────────────────────────────────────────
// Load limits
// ─────────────────────────────────────────────────────────────

func (c *Config) loadNewPlanLimits() {
	c.NCPU, _ = strconv.Atoi(c.getPlanLimit(c.NewPlanID, "cpu"))

	c.NRAM = c.getPlanLimit(c.NewPlanID, "ram")
	c.NRAMNum, _ = strconv.Atoi(strings.TrimSuffix(c.NRAM, "g"))

	diskStr := strings.Fields(c.getPlanLimit(c.NewPlanID, "disk_limit"))
	if len(diskStr) > 0 {
		c.NDiskLimit, _ = strconv.Atoi(diskStr[0])
	}
	c.StorageBlocks = c.NDiskLimit * 1024000

	c.NInodes, _ = strconv.Atoi(c.getPlanLimit(c.NewPlanID, "inodes_limit"))
	c.NBandwidth = c.getPlanLimit(c.NewPlanID, "bandwidth")
}

func (c *Config) loadServerCaps() {
	out, _ := runCmd("nproc")
	c.MaxCPU, _ = strconv.Atoi(strings.TrimSpace(out))

	out, _ = runCmd("bash", "-c", "free -g | awk '/^Mem/ {print $2}'")
	c.MaxRAM, _ = strconv.Atoi(strings.TrimSpace(out))
}

func (c *Config) loadOldLimits() {
	// Try compose file first, fall back to DB
	c.OCPU = readComposeValue(c.ComposeFile, "cpu")
	if c.OCPU == "" {
		fmt.Printf("Warning: Key 'cpu' not found in %s.\n", c.ComposeFile)
		c.OCPU = c.getPlanLimit(c.CurrentPlanID, "cpu")
	}

	c.ORAM = readComposeValue(c.ComposeFile, "ram")
	if c.ORAM == "" {
		fmt.Printf("Warning: Key 'ram' not found in %s.\n", c.ComposeFile)
		c.ORAM = c.getPlanLimit(c.CurrentPlanID, "ram")
	}
}

// ─────────────────────────────────────────────────────────────
// Update CPU
// ─────────────────────────────────────────────────────────────

func (c *Config) updateContainerCPU() {
	if c.NCPU > c.MaxCPU {
		fmt.Printf("Error: New CPU value exceeds the server limit, not enough CPU cores - %d > %d.\n", c.NCPU, c.MaxCPU)
		os.Exit(1)
	}

	if c.Debug {
		fmt.Printf("Updating total CPU%% limit from: %s to %d\n", c.OCPU, c.NCPU)
	}

	_, err := runCmd("opencli", "user-resources", c.ContainerName,
		fmt.Sprintf("--update_cpu=%d", c.NCPU))
	if err != nil {
		c.FailureCount++
		fmt.Println("[✘] Error setting total CPU limit for the user:")
		fmt.Printf("Command used: opencli user-resources \"%s\" --update_cpu=\"%d\"\n", c.ContainerName, c.NCPU)
	} else {
		c.SuccessCount++
		fmt.Printf("[✔] Total CPU limit (%d) changed successfully for container.\n", c.NCPU)
	}
}

// ─────────────────────────────────────────────────────────────
// Update RAM
// ─────────────────────────────────────────────────────────────

func (c *Config) updateContainerRAM() {
	if c.NRAMNum > c.MaxRAM {
		fmt.Printf("Warning: Ram limit not changed for the contianer -new value exceeds the server limit, not enough physical memory - %d > %d.\n", c.NRAMNum, c.MaxRAM)
		return
	}

	if c.Debug {
		fmt.Printf("Updating Memory limit from: %s to %s\n", c.ORAM, c.NRAM)
	}

	_, err := runCmd("opencli", "user-resources", c.ContainerName,
		fmt.Sprintf("--update_ram=%s", c.NRAM))
	if err != nil {
		c.FailureCount++
		fmt.Println("[✘] Error setting total RAM limit for user:")
		fmt.Printf("Command used: opencli user-resources \"%s\" --update_ram=\"%s\"\n", c.ContainerName, c.NRAM)
	} else {
		c.SuccessCount++
		fmt.Printf("[✔] Total Memory limit %s changed successfully for user\n", c.NRAM)
	}
}

// ─────────────────────────────────────────────────────────────
// Update disk quota + inodes
// ─────────────────────────────────────────────────────────────

func (c *Config) updateDiskAndInodes() {
	if c.Debug {
		fmt.Printf("Changing disk limit from: (old) to %d (%d)\n", c.NDiskLimit, c.StorageBlocks)
		fmt.Printf("Changing inodes limit from: (old) to %d\n", c.NInodes)
	}

	_, err := runCmd("setquota", "-u", c.ContainerName,
		strconv.Itoa(c.StorageBlocks), strconv.Itoa(c.StorageBlocks),
		strconv.Itoa(c.NInodes), strconv.Itoa(c.NInodes), "/")
	if err != nil {
		c.FailureCount++
		fmt.Println("[✘] Error setting disk and inodes limits for the user:")
		fmt.Printf("Command used: setquota -u %s %d %d %d %d /\n",
			c.ContainerName, c.StorageBlocks, c.StorageBlocks, c.NInodes, c.NInodes)
	} else {
		c.SuccessCount++
		fmt.Printf("[✔] Disk usage limit: %d and inodes limit: %d applied successfully to the user.\n",
			c.NDiskLimit, c.NInodes)
	}

	runCmdSilent("quotacheck", "-avm")
	runCmd("bash", "-c", "repquota -u / > /etc/openpanel/openpanel/core/users/repquota")
}

// ─────────────────────────────────────────────────────────────
// Update plan in DB
// ─────────────────────────────────────────────────────────────

func (c *Config) changePlanNameInDB() {
	if c.Debug {
		fmt.Printf("Changing plan name for user from '%s' to: '%s'\n", c.CurrentPlanName, c.NewPlanName)
	}

	query := fmt.Sprintf("UPDATE users SET plan_id = %s WHERE username = '%s';", c.NewPlanID, c.ContainerName)
	script := fmt.Sprintf(
		`source %s && mysql -D "$mysql_database" -N -B -e %q`,
		dbConfigFile, query,
	)
	_, err := runCmd("bash", "-c", script)
	if err != nil {
		fmt.Println("Error changing plan id in the database for user - is mysql service running?")
		return
	}

	if c.FailureCount > 0 {
		fmt.Printf("Plan changed successfuly for user %s from %s to %s - (%d warnings)\n",
			c.ContainerName, c.CurrentPlanName, c.NewPlanName, c.FailureCount)
	} else {
		fmt.Printf("Plan changed successfuly for user %s from %s to %s\n",
			c.ContainerName, c.CurrentPlanName, c.NewPlanName)
	}
}

// ─────────────────────────────────────────────────────────────
// Drop Redis cache (background)
// ─────────────────────────────────────────────────────────────

func (c *Config) dropRedisCache() {
	cmd := exec.Command("docker", "--context=default", "exec", "openpanel_redis",
		"bash", "-c", "redis-cli --raw KEYS 'flask_cache_*' | xargs -r redis-cli DEL")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Start() // fire and forget
}

// ─────────────────────────────────────────────────────────────
// Final check — warn if compose writes failed
// ─────────────────────────────────────────────────────────────

func (c *Config) tada() {
	if c.WriteFailureCount > 0 {
		fmt.Println()
		fmt.Printf("Error changing %d values in file: %s\n", c.WriteFailureCount, c.ComposeFile)
		if c.Debug {
			data, _ := os.ReadFile(c.ComposeFile)
			fmt.Println("Current values:")
			fmt.Println(string(data))
		}
	}
}

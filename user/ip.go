////////////////////////////////////////////////////////////////////////////////
// Script Name: user/ip.go
// Description: Assign or remove dedicated IP for a user.
// Usage: opencli user-ip <USERNAME> <IP | delete> [-y] [--debug]
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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const (
	jsonFileBase  = "/etc/openpanel/openpanel/core/users"
	caddyConfPath = "/etc/openpanel/caddy/domains"
	zoneFilePath  = "/etc/bind/zones"
)

// ─────────────────────────────────────────────────────────────
// Config / state
// ─────────────────────────────────────────────────────────────

type Config struct {
	Username    string
	Action      string // IP address or "delete"
	ConfirmFlag bool   // -y
	Debug       bool
	ServerIP    string
	AllowedIPs  []string
}

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	cfg := &Config{}
	positional := []string{}

	for _, arg := range args {
		switch arg {
		case "--debug":
			cfg.Debug = true
		case "-y":
			cfg.ConfirmFlag = true
		default:
			positional = append(positional, arg)
		}
	}

	if len(positional) < 1 {
		printUsage()
		os.Exit(1)
	}

	cfg.Username = positional[0]
	if len(positional) >= 2 {
		cfg.Action = positional[1]
	}

	cfg.ServerIP = getServerIP()
	cfg.AllowedIPs = getAllowedIPs()

	ensureJQInstalled()

	// No action: print current IP
	if cfg.Action == "" {
		fmt.Println(cfg.getCurrentIP())
		os.Exit(0)
	}

	var ipToUse string

	if strings.ToLower(cfg.Action) == "delete" {
		cfg.deleteIPConfig()
		ipToUse = cfg.ServerIP
	} else {
		ip := cfg.Action
		cfg.checkIPValidity(ip)
		cfg.checkIPUsage(ip)
		ipToUse = ip
	}

	cfg.editDomainFiles(ipToUse)

	if strings.ToLower(cfg.Action) == "delete" {
		// Remove the file a second time in case edit_domain_files recreated it
		jsonFile := filepath.Join(jsonFileBase, cfg.Username, "ip.json")
		os.Remove(jsonFile)
		fmt.Printf("IP successfully changed for user %s to shared IP address: %s\n", cfg.Username, ipToUse)
		dropRedisCache()
	} else {
		if err := cfg.createIPFile(ipToUse); err != nil {
			fmt.Printf("Failed to set dedicated IP address for user %s.\n", cfg.Username)
			os.Exit(1)
		}
		fmt.Printf("IP successfully changed for user %s to dedicated IP address: %s\n", cfg.Username, ipToUse)
		dropRedisCache()
	}
}

// ─────────────────────────────────────────────────────────────
// Usage
// ─────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Println("Usage: opencli user-ip <USERNAME> <ACTION> [ -y ] [--debug]")
	fmt.Println()
	fmt.Println("Assign Dedicated IP to a user: opencli user-ip <USERNAME> <IP_ADDRESS> [ -y ] [--debug]")
	fmt.Println("Remove Dedicated IP from user: opencli user-ip <USERNAME> delete [ -y ] [--debug]")
}

// ─────────────────────────────────────────────────────────────
// Network helpers
// ─────────────────────────────────────────────────────────────

func getServerIP() string {
	out, err := exec.Command("hostname", "-I").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// getAllowedIPs returns all IPs from `hostname -I` excluding 172.x.x.x ranges.
func getAllowedIPs() []string {
	out, err := exec.Command("hostname", "-I").Output()
	if err != nil {
		return nil
	}
	var allowed []string
	for _, ip := range strings.Fields(string(out)) {
		if !strings.HasPrefix(ip, "172.") {
			allowed = append(allowed, ip)
		}
	}
	return allowed
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func runCmdBackground(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Start()
}

// ─────────────────────────────────────────────────────────────
// Ensure jq is available
// ─────────────────────────────────────────────────────────────

func ensureJQInstalled() {
	if _, err := exec.LookPath("jq"); err == nil {
		return
	}
	if _, err := exec.LookPath("apt-get"); err == nil {
		exec.Command("apt-get", "update", "-qq").Run()
		exec.Command("apt-get", "install", "-y", "-qq", "jq").Run()
	} else if _, err := exec.LookPath("yum"); err == nil {
		exec.Command("yum", "install", "-y", "-q", "jq").Run()
	} else if _, err := exec.LookPath("dnf"); err == nil {
		exec.Command("dnf", "install", "-y", "-q", "jq").Run()
	} else {
		fmt.Println("Error: No compatible package manager found. Please install jq manually.")
		os.Exit(1)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		fmt.Println("jq installation failed.")
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────
// IP file helpers
// ─────────────────────────────────────────────────────────────

func (c *Config) getCurrentIP() string {
	jsonFile := filepath.Join(jsonFileBase, c.Username, "ip.json")
	if !fileExists(jsonFile) {
		return c.ServerIP
	}
	data, err := os.ReadFile(jsonFile)
	if err != nil {
		return c.ServerIP
	}
	var obj struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(data, &obj); err != nil || obj.IP == "" {
		return c.ServerIP
	}
	return obj.IP
}

func (c *Config) createIPFile(ip string) error {
	jsonFile := filepath.Join(jsonFileBase, c.Username, "ip.json")
	content := fmt.Sprintf("{ \"ip\": \"%s\" }\n", ip)
	if err := os.WriteFile(jsonFile, []byte(content), 0644); err != nil {
		return err
	}
	if c.Debug {
		fmt.Printf("Created IP file %s with IP %s\n", jsonFile, ip)
	}
	return nil
}

func (c *Config) deleteIPConfig() {
	jsonFile := filepath.Join(jsonFileBase, c.Username, "ip.json")
	if fileExists(jsonFile) {
		os.Remove(jsonFile)
		fmt.Printf("IP configuration deleted for user %s.\n", c.Username)
	}
}

// ─────────────────────────────────────────────────────────────
// Validation
// ─────────────────────────────────────────────────────────────

func (c *Config) checkIPValidity(ip string) {
	// Must parse as a valid IP
	if net.ParseIP(ip) == nil {
		fmt.Printf("Error: The provided IP address is not allowed. Must be one of: %s\n",
			strings.Join(c.AllowedIPs, " "))
		os.Exit(1)
	}
	for _, allowed := range c.AllowedIPs {
		if allowed == ip {
			return
		}
	}
	fmt.Printf("Error: The provided IP address is not allowed. Must be one of: %s\n",
		strings.Join(c.AllowedIPs, " "))
	os.Exit(1)
}

func (c *Config) checkIPUsage(ip string) {
	entries, err := os.ReadDir(jsonFileBase)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == c.Username {
			continue
		}
		userJSON := filepath.Join(jsonFileBase, entry.Name(), "ip.json")
		if !fileExists(userJSON) {
			continue
		}
		data, err := os.ReadFile(userJSON)
		if err != nil {
			continue
		}
		var obj struct {
			IP string `json:"ip"`
		}
		if err := json.Unmarshal(data, &obj); err != nil {
			continue
		}
		if obj.IP == ip {
			if !c.ConfirmFlag {
				fmt.Printf("Error: IP %s already assigned to user %s.\n", ip, entry.Name())
				fmt.Print("Are you sure you want to continue? (y/n): ")
				scanner := bufio.NewScanner(os.Stdin)
				scanner.Scan()
				answer := strings.TrimSpace(scanner.Text())
				if answer != "y" {
					fmt.Println("Script aborted.")
					os.Exit(1)
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Caddy / DNS editing
// ─────────────────────────────────────────────────────────────

// updateBindInBlock mirrors the bash function:
// If the line after block_header already has "bind X", replace it.
// Otherwise insert "    bind X" after block_header.
func updateBindInBlock(confPath, blockHeader, ip string) error {
	data, err := os.ReadFile(confPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+1)
	i := 0
	for i < len(lines) {
		line := lines[i]
		out = append(out, line)
		if strings.TrimSpace(line) == strings.TrimSpace(blockHeader) {
			// Look at the next line
			if i+1 < len(lines) && strings.Contains(lines[i+1], "bind ") {
				// Replace existing bind directive
				i++
				out = append(out, "    bind "+ip)
			} else {
				// Insert bind directive after header
				out = append(out, "    bind "+ip)
			}
		}
		i++
	}

	return os.WriteFile(confPath, []byte(strings.Join(out, "\n")), 0644)
}

func (c *Config) editDomainFiles(ip string) {
	domainsOut, _ := runCmd("opencli", "domains-user", c.Username)
	domains := strings.Fields(domainsOut)

	currentIP := c.getCurrentIP()

	caddyChanged := false
	bindChanged := false

	for _, domain := range domains {
		caddyConf := filepath.Join(caddyConfPath, domain+".conf")
		if fileExists(caddyConf) {
			updateBindInBlock(caddyConf, fmt.Sprintf("http://%s, http://*.%s {", domain, domain), ip)
			updateBindInBlock(caddyConf, fmt.Sprintf("https://%s, https://*.%s {", domain, domain), ip)
			if c.Debug {
				fmt.Printf("- Updated Caddy configuration for %s to %s\n", domain, ip)
			}
			caddyChanged = true
		}

		bindConf := filepath.Join(zoneFilePath, domain+".zone")
		if fileExists(bindConf) {
			replaceInFile(bindConf, currentIP, ip)
			if c.Debug {
				fmt.Printf("- Updated DNS zone file %s with IP %s\n", bindConf, ip)
			}
			bindChanged = true
		}
	}

	if caddyChanged {
		runCmd("docker", "--context=default", "exec", "caddy",
			"bash", "-c", "caddy validate && caddy reload")
		if c.Debug {
			fmt.Println("- Reloaded webserver")
		}
	}

	if bindChanged {
		if err := runCmdSilent("docker", "--context=default", "restart", "openpanel_bind9"); err != nil {
			runCmdSilent("service", "bind9", "restart")
		}
		if c.Debug {
			fmt.Println("- Restarted DNS server")
		}
	}
}

// replaceInFile does a global string replacement in a file (mirrors sed -i "s/old/new/g").
func replaceInFile(path, old, new string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated := strings.ReplaceAll(string(data), old, new)
	return os.WriteFile(path, []byte(updated), 0644)
}

// ─────────────────────────────────────────────────────────────
// Redis cache drop (background)
// ─────────────────────────────────────────────────────────────

func dropRedisCache() {
	runCmdBackground("docker", "--context=default", "exec", "openpanel_redis",
		"bash", "-c", "redis-cli --raw KEYS 'flask_cache_*' | xargs -r redis-cli DEL")
}

// runCmdSilent runs a command discarding output, returns error.
func runCmdSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

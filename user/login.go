////////////////////////////////////////////////////////////////////////////////
// Script Name: user/login.go
// Description: Generate an auto-login link for OpenPanel user.
// Usage: opencli user-login <username> [--open] [--delete]
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
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────

const (
	userDirBase   = "/etc/openpanel/openpanel/core/users"
	caddyFile     = "/etc/openpanel/caddy/Caddyfile"
	ipServersScript = "/usr/local/opencli/ip_servers.sh"
	defaultPort   = "2083"
)

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	if len(args) < 1 {
		fmt.Println("Usage: opencli user-login <username>")
		os.Exit(1)
	}

	username := args[0]
	nowFlag := false
	deleteFlag := false

	for _, arg := range args[1:] {
		switch arg {
		case "--open":
			nowFlag = true
		case "--delete":
			deleteFlag = true
		}
	}

	// 1. Verify user directory exists
	userDir := filepath.Join(userDirBase, username)
	if !dirExists(userDir) {
		fmt.Printf("[✘] Error: Username '%s' does not exist or was not properly created (missing files).\n", username)
		os.Exit(1)
	}

	// 2. Read existing or generate a new token
	tokenFile := filepath.Join(userDir, "logintoken.txt")
	var adminToken string

	if fileExists(tokenFile) {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			fmt.Printf("[✘] Error: Cannot read token file: %v\n", err)
			os.Exit(1)
		}
		adminToken = strings.TrimSpace(string(data))

		if deleteFlag {
			os.Remove(tokenFile)
			fmt.Printf("Auto-login token '%s' for user %s is now invalidated.\n", adminToken, username)
			os.Exit(0)
		}
	} else {
		if deleteFlag {
			fmt.Printf("No auto-login token exists for user %s.\n", username)
			os.Exit(0)
		}
		// Generate new token
		os.MkdirAll(filepath.Dir(tokenFile), 0755)
		adminToken = generateToken(30)
		if err := os.WriteFile(tokenFile, []byte(adminToken+"\n"), 0600); err != nil {
			fmt.Printf("[✘] Error: Cannot write token file: %v\n", err)
			os.Exit(1)
		}
	}

	// 3. Build login URL
	openpanelURL := getOpenPanelURL()
	loginURL := openpanelURL + "login_autologin?" +
		"admin_token=" + urlEncode(adminToken) +
		"&username=" + urlEncode(username)

	// 4. Print link
	fmt.Println(loginURL)

	// 5. Optionally open in browser
	if nowFlag {
		if _, err := exec.LookPath("xdg-open"); err == nil {
			exec.Command("xdg-open", loginURL).Start()
		} else {
			fmt.Println("xdg-open not found, cannot open URL automatically.")
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Token generation
// ─────────────────────────────────────────────────────────────

const tokenChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func generateToken(n int) string {
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(tokenChars))))
		b[i] = tokenChars[idx.Int64()]
	}
	return string(b)
}

// ─────────────────────────────────────────────────────────────
// URL encoding (mirrors the bash urlencode function)
// ─────────────────────────────────────────────────────────────

func urlEncode(s string) string {
	// url.QueryEscape encodes space as '+'; the bash version uses %20-style percent encoding.
	// url.PathEscape is closer but doesn't encode all chars. We use QueryEscape then fix '+'.
	encoded := url.QueryEscape(s)
	return strings.ReplaceAll(encoded, "+", "%20")
}

// ─────────────────────────────────────────────────────────────
// OpenPanel URL resolution
// ─────────────────────────────────────────────────────────────

func getOpenPanelURL() string {
	port := getPort()

	// Read IP server list from script if available
	ip1, ip2, ip3 := getIPServers()

	// Extract domain from Caddyfile between hostname markers
	domain := extractHostnameDomain()

	if domain == "" || domain == "example.net" {
		ip := getPublicIP(ip1, ip2, ip3)
		return fmt.Sprintf("http://%s:%s/", ip, port)
	}

	// Check for SSL certificates
	leCert := fmt.Sprintf("/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/%s/%s.crt", domain, domain)
	leKey := fmt.Sprintf("/etc/openpanel/caddy/ssl/acme-v02.api.letsencrypt.org-directory/%s/%s.key", domain, domain)
	customCert := fmt.Sprintf("/etc/openpanel/caddy/ssl/custom/%s/%s.crt", domain, domain)
	customKey := fmt.Sprintf("/etc/openpanel/caddy/ssl/custom/%s/%s.key", domain, domain)

	if (fileExists(leCert) && fileExists(leKey)) || (fileExists(customCert) && fileExists(customKey)) {
		return fmt.Sprintf("https://%s:%s/", domain, port)
	}

	ip := getPublicIP(ip1, ip2, ip3)
	return fmt.Sprintf("http://%s:%s/", ip, port)
}

func getPort() string {
	out, err := exec.Command("opencli", "port").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return defaultPort
	}
	return strings.TrimSpace(string(out))
}

// getIPServers sources ip_servers.sh and reads IP_SERVER_1/2/3 if available.
func getIPServers() (string, string, string) {
	fallback := "https://ip.openpanel.com"
	if !fileExists(ipServersScript) {
		return fallback, fallback, fallback
	}
	script := fmt.Sprintf(`source %s && echo "$IP_SERVER_1" && echo "$IP_SERVER_2" && echo "$IP_SERVER_3"`, ipServersScript)
	out, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		return fallback, fallback, fallback
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	get := func(i int) string {
		if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
		return fallback
	}
	return get(0), get(1), get(2)
}

// extractHostnameDomain reads the Caddyfile and finds the domain between
// # START HOSTNAME DOMAIN # and # END HOSTNAME DOMAIN # markers.
func extractHostnameDomain() string {
	f, err := os.Open(caddyFile)
	if err != nil {
		return ""
	}
	defer f.Close()

	inBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "# START HOSTNAME DOMAIN #") {
			inBlock = true
			continue
		}
		if strings.Contains(line, "# END HOSTNAME DOMAIN #") {
			break
		}
		if !inBlock {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Strip trailing " {" and whitespace
		domain := regexp.MustCompile(`\s*\{.*$`).ReplaceAllString(trimmed, "")
		domain = strings.TrimSpace(domain)
		// Strip http(s)://
		domain = regexp.MustCompile(`^https?://`).ReplaceAllString(domain, "")
		return domain
	}
	return ""
}

// getPublicIP tries three IP servers in order, falls back to hostname -I.
func getPublicIP(server1, server2, server3 string) string {
	ipRegex := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

	for _, server := range []string{server1, server2, server3} {
		out, err := exec.Command("curl", "--silent", "--max-time", "2", "-4", server).Output()
		if err == nil {
			ip := strings.TrimSpace(string(out))
			if ipRegex.MatchString(ip) {
				return ip
			}
		}
	}

	// Fallback to local IP
	out, err := exec.Command("hostname", "-I").Output()
	if err == nil {
		fields := strings.Fields(string(out))
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
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

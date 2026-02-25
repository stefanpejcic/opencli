////////////////////////////////////////////////////////////////////////////////
// Script Name: user/varnish.go
// Description: Enable/disable Varnish Caching for user and display current status.
// Usage: opencli user-varnish <USERNAME> [enable|disable|status]
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
	"strings"
)

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	if len(args) < 1 {
		fmt.Println("Usage: opencli user-varnish <user> [enable|disable|status]")
		os.Exit(1)
	}

	user := args[0]
	action := ""
	if len(args) >= 2 {
		action = strings.ToLower(args[1])
	}

	envFile := fmt.Sprintf("/home/%s/.env", user)
	if !fileExists(envFile) {
		fmt.Printf("Error: %s not found!\n", envFile)
		os.Exit(1)
	}

	if action == "" {
		checkStatus(envFile)
		return
	}

	ws := getWebserverForUser(user)

	switch action {
	case "enable":
		stopWebserver(user, ws)
		uncommentProxyPort(envFile)
		startWebserver(user, ws)
		startVarnish(user)
		checkVarnish(user, "start")
	case "disable":
		stopWebserver(user, ws)
		commentProxyPort(envFile)
		stopVarnish(user)
		startWebserver(user, ws)
		checkVarnish(user, "stop")
	case "status":
		checkVarnish(user, "status")
	default:
		fmt.Println("Invalid action. Use 'status', 'enable' or 'disable'.")
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ─────────────────────────────────────────────────────────────
// Status (no action supplied — reads .env directly)
// ─────────────────────────────────────────────────────────────

func checkStatus(envFile string) {
	f, err := os.Open(envFile)
	if err != nil {
		fmt.Printf("Error: cannot open %s\n", envFile)
		os.Exit(1)
	}
	defer f.Close()

	status := "Unknown"
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#PROXY_HTTP_PORT=") {
			status = "Off"
			break
		}
		if strings.HasPrefix(line, "PROXY_HTTP_PORT=") {
			status = "On"
			break
		}
	}

	fmt.Printf("Current status: %s\n", status)
}

// ─────────────────────────────────────────────────────────────
// Detect webserver from opencli helper
// ─────────────────────────────────────────────────────────────

func getWebserverForUser(user string) string {
	out, _ := runCmd("opencli", "webserver-get_webserver_for_user", user)
	for _, token := range strings.Fields(out) {
		switch token {
		case "nginx", "openresty", "apache", "openlitespeed", "litespeed":
			return token
		}
	}
	return ""
}

// ─────────────────────────────────────────────────────────────
// Docker Compose wrappers
// ─────────────────────────────────────────────────────────────

func dockerCompose(user string, args ...string) (string, error) {
	baseArgs := []string{"--context", user, "compose"}
	baseArgs = append(baseArgs, args...)
	cmd := exec.Command("docker", baseArgs...)
	cmd.Dir = fmt.Sprintf("/home/%s", user)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func stopWebserver(user, ws string) {
	if ws != "" {
		dockerCompose(user, "down", ws)
	}
}

func startWebserver(user, ws string) {
	if ws != "" {
		dockerCompose(user, "up", "-d", ws)
	}
}

func stopVarnish(user string) {
	out, err := dockerCompose(user, "down", "varnish")
	if err != nil {
		fmt.Println(out)
	}
}

func startVarnish(user string) {
	out, err := dockerCompose(user, "up", "-d", "varnish")
	if err != nil {
		fmt.Println(out)
	}
}

// ─────────────────────────────────────────────────────────────
// Check running state and print result
// ─────────────────────────────────────────────────────────────

func checkVarnish(user, action string) {
	out, _ := dockerCompose(user, "ps", "-q", "varnish")
	running := strings.TrimSpace(out) != ""

	switch action {
	case "start":
		if running {
			fmt.Println("Varnish Cache is now enabled.")
		} else {
			fmt.Println("Failed to enable Varnish Cache.")
		}
	case "stop":
		if !running {
			fmt.Println("Varnish Cache is now disabled.")
		} else {
			fmt.Println("Failed to disable Varnish Cache.")
		}
	case "status":
		if running {
			fmt.Println("Varnish Cache is enabled.")
		} else {
			fmt.Println("Varnish Cache is disabled.")
		}
	default:
		fmt.Println("Usage: status_varnish {start|stop|status}")
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────
// .env file editing — toggle PROXY_HTTP_PORT comment
// ─────────────────────────────────────────────────────────────

// uncommentProxyPort turns  #PROXY_HTTP_PORT=...  →  PROXY_HTTP_PORT=...
func uncommentProxyPort(envFile string) {
	rewriteEnvLine(envFile, func(line string) string {
		if strings.HasPrefix(line, "#PROXY_HTTP_PORT=") {
			return strings.TrimPrefix(line, "#")
		}
		return line
	})
}

// commentProxyPort turns  PROXY_HTTP_PORT=...  →  #PROXY_HTTP_PORT=...
func commentProxyPort(envFile string) {
	rewriteEnvLine(envFile, func(line string) string {
		if strings.HasPrefix(line, "PROXY_HTTP_PORT=") {
			return "#" + line
		}
		return line
	})
}

// rewriteEnvLine rewrites the file line-by-line applying transform to each line.
func rewriteEnvLine(envFile string, transform func(string) string) {
	f, err := os.Open(envFile)
	if err != nil {
		fmt.Printf("Error: cannot open %s: %v\n", envFile, err)
		os.Exit(1)
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, transform(scanner.Text()))
	}
	f.Close()

	out, err := os.OpenFile(envFile, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Printf("Error: cannot write %s: %v\n", envFile, err)
		os.Exit(1)
	}
	defer out.Close()

	w := bufio.NewWriter(out)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
	w.Flush()
}

////////////////////////////////////////////////////////////////////////////////
// Script Name: user/loginlog.go
// Description: View user's .lastlogin file with last 20 successful logins.
// Usage: opencli user-loginlog <USERNAME> [--table|--text|--json]
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
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ─────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────

type LoginEntry struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	Time    string `json:"time"`
}

// ─────────────────────────────────────────────────────────────
// main
// ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args[1:]

	username := ""
	jsonOutput := false
	tableOutput := true // default
	textOutput := false

	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
			tableOutput = false
		case "--table":
			tableOutput = true
			jsonOutput = false
			textOutput = false
		case "--text":
			textOutput = true
			tableOutput = false
			jsonOutput = false
		default:
			if strings.HasPrefix(arg, "-") {
				printUsage()
			}
			username = arg
		}
	}

	if username == "" {
		printUsage()
	}

	loginLogFile := fmt.Sprintf("/etc/openpanel/openpanel/core/users/%s/.lastlogin", username)
	if !fileExists(loginLogFile) {
		fmt.Printf("Login log file not found for user: %s\n", username)
		os.Exit(1)
	}

	entries := parseLoginLog(loginLogFile)

	switch {
	case tableOutput:
		printTable(entries)
	case jsonOutput:
		printJSON(entries)
	case textOutput:
		printText(loginLogFile)
	}
}

// ─────────────────────────────────────────────────────────────
// Usage
// ─────────────────────────────────────────────────────────────

func printUsage() {
	fmt.Println("Usage: opencli user-loginlog <username> [--json | --table]")
	os.Exit(1)
}

// ─────────────────────────────────────────────────────────────
// Parser
// Log line format: "IP: 1.2.3.4 - Country: US - Time: 2024-01-01 12:00:00"
// ─────────────────────────────────────────────────────────────

func parseLoginLog(path string) []LoginEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []LoginEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry := parseLine(line)
		entries = append(entries, entry)
	}
	return entries
}

// parseLine splits on " - " then on ": " to extract field values,
// matching the awk logic in the original script.
func parseLine(line string) LoginEntry {
	parts := strings.SplitN(line, " - ", 3)
	get := func(i int) string {
		if i >= len(parts) {
			return ""
		}
		kv := strings.SplitN(parts[i], ": ", 2)
		if len(kv) == 2 {
			return kv[1]
		}
		return ""
	}
	return LoginEntry{
		IP:      get(0),
		Country: get(1),
		Time:    get(2),
	}
}

// ─────────────────────────────────────────────────────────────
// Output: table (default)
// Mirrors: { echo -e "IP\tCountry\tTime"; awk ... } | column -t -s $'\t'
// ─────────────────────────────────────────────────────────────

func printTable(entries []LoginEntry) {
	// Collect all rows including header to calculate column widths
	rows := make([][3]string, 0, len(entries)+1)
	rows = append(rows, [3]string{"IP", "Country", "Time"})
	for _, e := range entries {
		rows = append(rows, [3]string{e.IP, e.Country, e.Time})
	}

	// Calculate max width per column
	widths := [3]int{}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print with padding (mirrors `column -t -s $'\t'`)
	for _, row := range rows {
		fmt.Printf("%-*s  %-*s  %s\n",
			widths[0], row[0],
			widths[1], row[1],
			row[2],
		)
	}
}

// ─────────────────────────────────────────────────────────────
// Output: JSON
// ─────────────────────────────────────────────────────────────

func printJSON(entries []LoginEntry) {
	if entries == nil {
		entries = []LoginEntry{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "\t")
	enc.Encode(entries)
}

// ─────────────────────────────────────────────────────────────
// Output: text (raw file + trailing newline)
// ─────────────────────────────────────────────────────────────

func printText(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(data))
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

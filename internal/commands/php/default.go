// ##############################################################################
// Script Name: internal/commands/php/default.go
// Description: View or change the default PHP version used for new domains added by user.
// Usage: opencli php-default <USERNAME> [--update <PHP_VERSION>]
// Author: Stefan Pejcic
// Created: 07.10.2023
// Last Modified: 20.11.2026
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
// ##############################################################################

package php

import (
	"fmt"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "php-default",
		Desc:  "View or change the default PHP version used for new domains added by user.",
		Usage: "opencli php-default <USERNAME> [--update <PHP_VERSION>]",
		Run:   defaultVersion,
	})
}

var versionRe = regexp.MustCompile(`^[0-9]\.[0-9]$`)

func defaultVersion(args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: opencli php-default <username> [--update <new_php_version>]")
		return 1
	}
	username := args[0]
	db.Require()

	info := text.FirstLine(db.Plain("SELECT id, server FROM users WHERE username = '"+db.Escape(username)+"';", "-s").NoErr().Str())
	if text.Field(info, 1) == "" {
		fmt.Printf("FATAL ERROR: user %s does not exist.\n", username)
		return 1
	}
	envFile := "/home/" + text.Field(info, 2) + "/.env"
	if !conf.Exists(envFile) {
		fmt.Printf("Configuration file for user '%s' not found.\n", username)
		return 1
	}

	if cli.Arg(args, 1) == "--update" {
		v := cli.Arg(args, 2)
		if v == "" {
			fmt.Println("Usage: opencli php-default <username> --update <new_php_version>")
			return 1
		}
		if !versionRe.MatchString(strings.TrimSuffix(v, "\n")) {
			fmt.Println("Invalid PHP version format. Please use the format 'number.number' (e.g., 8.1 or 5.6).")
			return 1
		}
		conf.SedFile(envFile, func(lines []string) []string {
			for i, l := range lines {
				if strings.HasPrefix(l, "DEFAULT_PHP_VERSION=") {
					lines[i] = "DEFAULT_PHP_VERSION=" + v
				}
			}
			return lines
		})
		fmt.Printf("Default PHP version for user '%s' updated to: %s\n", username, v)
		return 0
	}

	// awk -F= on every line mentioning the key, second field, whitespace dropped
	version := ""
	for _, l := range conf.Lines(envFile) {
		if strings.Contains(l, "DEFAULT_PHP_VERSION") {
			if parts := strings.Split(l, "="); len(parts) > 1 {
				version += parts[1]
			}
		}
	}
	version = text.NoSpace(version)
	version = strings.TrimPrefix(version, "php") // legacy for <0.3.4
	if version != "" {
		fmt.Printf("Default PHP version for user '%s' is: %s\n", username, version)
		return 0
	}
	fmt.Printf("Default PHP version for user: '%s' not found in the configuration file.\n", username)
	return 1
}

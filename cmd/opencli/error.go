// ##############################################################################
// Script Name: cmd/opencli/error.go
// Description: Displays information for specific error ID received in OpenPanel UI.
// Usage: opencli error <ID_HERE>
// Author: Stefan Pejcic
// Created: 20.11.2026
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

package main

import (
	"fmt"
	"os"
	"strings"

	"opencli/internal/sh"
)

// errorCommand replaces error.py, see https://github.com/stefanpejcic/opencli/issues/63
func errorCommand(args []string) int {
	var pos []string
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Println("usage: error.py [-h] error_code\n\nExtract error logs from the OpenPanel container by error code.\n\npositional arguments:\n  error_code  The error code to search for in the logs\n\noptions:\n  -h, --help  show this help message and exit")
			return 0
		}
		pos = append(pos, a)
	}
	if len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "usage: error.py [-h] error_code")
		if len(pos) == 0 {
			fmt.Fprintln(os.Stderr, "error.py: error: the following arguments are required: error_code")
		} else {
			fmt.Fprintf(os.Stderr, "error.py: error: unrecognized arguments: %s\n", strings.Join(pos[1:], " "))
		}
		// the old dispatcher exited 0 after error.py no matter what
		return 0
	}
	code := pos[0]
	out, err := sh.C("podman", "logs", "--since=60m", "openpanel").Combined()
	if err != nil {
		fmt.Printf("Error running podman logs: %s\n", out+"\n")
		fmt.Printf("Error while fetching logs: %s\n", out+"\n")
		return 0
	}
	lines := strings.Split(out, "\n")
	var result []string
	found := false
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if found {
			result = append(result, strings.TrimSpace(l))
			if strings.Contains(l, "ERROR") {
				break
			}
		} else if strings.Contains(strings.ToLower(l), strings.ToLower(code)) {
			found = true
			result = append(result, strings.TrimSpace(l))
		}
	}
	if !found {
		fmt.Println("\n=== NO LOGS FOR ERROR ID ===")
		fmt.Printf("Error Code '%s' not found in the OpenPanel UI logs.\n", code)
		return 0
	}
	fmt.Printf("\n=== LOGS FOR ERROR ID: '%s' ===\n\n", code)
	for i := len(result) - 1; i >= 0; i-- {
		fmt.Println(result[i])
	}
	return 0
}

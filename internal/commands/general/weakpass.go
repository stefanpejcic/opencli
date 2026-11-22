// ##############################################################################
// Script Name: internal/commands/general/weakpass.go
// Description: Checks a password from stdin against the weak-password list, exit 1 when it's common.
// Usage: opencli weakpass [--update]
// Author: Stefan Pejcic
// Created: 22.11.2026
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

package general

import (
	"bufio"
	"os"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/passwd"
)

// replaces lib/weakpass.sh, OpenPanel runs it inside its container with OPENCLI_RAW=1
func init() {
	cli.Register(&cli.Command{
		Name:   "weakpass",
		Desc:   "Checks a password from stdin against the weak-password list, exit 1 when it's common.",
		Usage:  "opencli weakpass [--update]",
		Hidden: true,
		Run:    weakpass,
	})
}

func weakpass(args []string) int {
	if len(args) > 0 && args[0] == "--update" {
		if passwd.WeakEnabled() {
			passwd.UpdateDict()
		}
		return 0
	}
	// read -r takes only the first line, without its newline
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if passwd.IsCommon(strings.TrimSuffix(line, "\n")) {
		return 1
	}
	return 0
}

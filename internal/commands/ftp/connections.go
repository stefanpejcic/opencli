// ##############################################################################
// Script Name: internal/commands/ftp/connections.go
// Description: Display all active FTP connections, or only those for domains owned by an OpenPanel user.
// Usage: opencli ftp-connections [OPENPANEL_USERNAME]
// Author: Stefan Pejcic
// Created: 11.09.2024
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

package ftp

import (
	"fmt"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "ftp-connections",
		Desc:  "Display all active FTP connections, or only those for domains owned by an OpenPanel user.",
		Usage: "opencli ftp-connections [OPENPANEL_USERNAME]",
		Docs:  "https://docs.openpanel.com",
		Run:   connections,
	})
}

const psCmd = `ps | grep "vsftpd:" | grep -w -v grep`

func connections(args []string) int {
	if len(args) > 1 {
		fmt.Println("Usage: opencli ftp-connections [openpanel_username]")
		return 1
	}
	if len(args) == 1 && args[0] != "" {
		// FTP sub-users are named user@domain, so only show connections for domains this user owns
		out := sh.Opencli("domains-user", args[0]).NoErr().Str()
		domRe := regexp.MustCompile(`^[A-Za-z0-9.-]+\.[A-Za-z0-9-]+$`)
		var doms []string
		for _, l := range strings.Split(out, "\n") {
			if domRe.MatchString(l) {
				doms = append(doms, strings.ReplaceAll(l, ".", `\.`))
			}
		}
		if len(doms) == 0 {
			cli.Fail("ERROR: No domains found for user '%s'. Aborting!", args[0])
		}
		re, err := regexp.Compile("@(" + strings.Join(doms, "|") + "):")
		raw, _ := ftpExec("sh", "-c", psCmd).Raw()
		found := false
		if err == nil {
			for _, l := range strings.SplitAfter(raw, "\n") {
				if l != "" && re.MatchString(l) {
					fmt.Print(l)
					if !strings.HasSuffix(l, "\n") {
						fmt.Println()
					}
					found = true
				}
			}
		}
		if !found {
			return 1
		}
		return 0
	}
	if err := ftpExec("sh", "-c", psCmd).Run(); err != nil {
		return sh.Code(err)
	}
	return 0
}

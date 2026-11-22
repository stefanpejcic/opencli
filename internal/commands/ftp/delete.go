// ##############################################################################
// Script Name: internal/commands/ftp/delete.go
// Description: Delete FTP sub-user for openpanel user.
// Usage: opencli ftp-delete <username> <openpanel_username> [--debug]
// Author: Stefan Pejcic
// Created: 22.05.2024
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
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "ftp-delete",
		Desc:  "Delete FTP sub-user for openpanel user.",
		Usage: "opencli ftp-delete <username> <openpanel_username> [--debug]",
		Docs:  "https://docs.openpanel.com",
		Run:   del,
	})
}

func del(args []string) int {
	if len(args) < 2 || len(args) > 3 {
		fmt.Println("Usage: opencli ftp-delete <username> <openpanel_username> [--debug]")
		return 1
	}
	username := strings.ToLower(args[0])
	opUser := args[1]
	// username goes into SQL and podman exec, so only the panel charset is accepted
	validName(username)
	ctx := contextFor(opUser, username)
	list := mkUsersList(ctx)

	if ftpExec("deluser", username).Run() == nil {
		if b, err := os.ReadFile(list); err == nil {
			re := regexp.MustCompile("^" + username + `\|`)
			lines := strings.SplitAfter(string(b), "\n")
			var out strings.Builder
			for _, l := range lines {
				if !re.MatchString(l) {
					out.WriteString(l)
				}
			}
			os.WriteFile(list, []byte(out.String()), 0o644)
		}
		// clears a leftover upload block in case the same username is added again
		ftpExec("/bin/start_vsftpd.sh", "--check-quotas").Quiet().Run()
		sh.Opencli("ftp-users").Quiet().Run()
		sentinelBg("--action=ftp_delete", "--title=FTP account deleted", "--message=FTP account '"+username+"' has been deleted.")
		fmt.Printf("Success: FTP user '%s' deleted successfully.\n", username)
		return 0
	}
	fmt.Printf("ERROR: Failed to delete FTP user. To debug run this command on terminal: opencli -x ftp-delete %s %s\n", username, opUser)
	return 1
}

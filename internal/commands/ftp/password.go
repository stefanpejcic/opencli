// ##############################################################################
// Script Name: internal/commands/ftp/password.go
// Description: Change password for FTP sub-user of openpanel user.
// Usage: opencli ftp-password <username> <new_password> <openpanel_username> [--debug]
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
	"strings"

	"opencli/internal/cli"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "ftp-password",
		Desc:  "Change password for FTP sub-user of openpanel user.",
		Usage: "opencli ftp-password <username> <new_password> <openpanel_username> [--debug]",
		Docs:  "https://docs.openpanel.com",
		Run:   password,
	})
}

func password(args []string) int {
	if len(args) < 3 || len(args) > 4 {
		fmt.Println("Usage: opencli ftp-password <username> <new_password> <openpanel_username> [--debug]")
		return 1
	}
	username := strings.ToLower(args[0])
	newPass := args[1]
	opUser := args[2]
	debug := hasFlag(args, "--debug")
	validName(username)

	ctx := contextFor(opUser, username)
	list := mkUsersList(ctx)
	if !hasUser(list, username) {
		cli.Fail("Error: FTP User '%s' does not exist.", username)
	}
	checkPassword(newPass, "new")

	hashed := hashPassword(newPass, false)
	if ftpExec("usermod", "-p", hashed, username).Run() == nil {
		// awk rewrite of the second field for this user
		b, _ := os.ReadFile(list)
		var out strings.Builder
		for _, l := range splitLines(string(b)) {
			f := strings.Split(l, "|")
			if f[0] == username {
				if len(f) < 2 {
					f = append(f, "")
				}
				f[1] = hashed
				l = strings.Join(f, "|")
			}
			out.WriteString(l + "\n")
		}
		tmp := "/tmp/" + ctx + ".ftp_users.list.tmp"
		if os.WriteFile(tmp, []byte(out.String()), 0o644) == nil {
			if os.Rename(tmp, list) != nil {
				os.WriteFile(list, []byte(out.String()), 0o644)
				os.Remove(tmp)
			}
		}
		sentinelBg("--action=ftp_password", "--title=FTP account password changed", "--message=FTP account '"+username+"' has password changed.")
		fmt.Printf("Success: FTP user '%s' password updated successfully.\n", username)
		return 0
	}
	if debug {
		fmt.Println("ERROR: Failed to update FTP user password with command:")
		fmt.Println("")
		fmt.Printf("podman exec openadmin_ftp usermod -p <hash> %s\n", username)
		fmt.Println("")
		fmt.Println("Run the command manually to check for errors.")
	} else {
		fmt.Printf("ERROR: Failed to update FTP user password. To debug run this command on terminal: opencli ftp-password %s %s %s --debug\n", username, newPass, opUser)
	}
	return 1
}

// splitLines is awk's record split, the final newline doesn't make an extra empty record
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

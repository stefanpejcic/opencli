// ##############################################################################
// Script Name: internal/commands/ftp/path.go
// Description: Change FTP path for a user.
// Usage: opencli ftp-path <username> <path> <openpanel_username> [--debug]
// Author: Stefan Pejcic
// Created: 10.09.2024
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
	"opencli/internal/tenantfs"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "ftp-path",
		Desc:  "Change FTP path for a user.",
		Usage: "opencli ftp-path <username> <path> <openpanel_username> [--debug]",
		Docs:  "https://docs.openpanel.com/",
		Run:   path,
	})
}

func path(args []string) int {
	if len(args) != 3 && len(args) != 4 {
		fmt.Println("Usage: opencli ftp-path <username> <path> <openpanel_username> [--debug]")
		return 1
	}
	username, p, opUser := args[0], args[1], args[2]
	debug := hasFlag(args, "--debug")
	validName(username)

	ctx := contextFor(opUser, username)
	os.MkdirAll(usersDir+ctx, 0o755)
	if !strings.HasPrefix(p, "/var/www/html/") {
		cli.Fail("ERROR: Invalid path. It must start with /var/www/html/")
	}
	if strings.Contains(p, "..") {
		cli.Fail("ERROR: Path traversal detected (.. is not allowed).")
	}
	if strings.Contains(p, "//") {
		cli.Fail("ERROR: Double slashes are not allowed in the path.")
	}
	if strings.Contains(p, "|") {
		cli.Fail("ERROR: The path cannot contain the '|' character.")
	}
	// the path also goes into users.list through sed, so keep it to plain path characters
	if !addDirRe.MatchString(p) {
		cli.Fail("ERROR: The path can only contain letters, numbers, dots, dashes, underscores and slashes.")
	}

	realPath := "/home/" + ctx + "/docker-data/volumes/" + ctx + "_html_data/_data/"
	cpath := realPath + strings.TrimPrefix(p, "/var/www/html/")
	// a symlink here would hand the FTP account someone else's files
	if _, err := tenantfs.Resolve(strings.TrimSuffix(realPath, "/"), cpath); err != nil {
		cli.Fail("ERROR: Path '%s' is not a real folder inside /var/www/html/.", p)
	}

	if ftpExec("usermod", "-d", cpath, username).Run() == nil {
		list := usersDir + ctx + "/users.list"
		if b, err := os.ReadFile(list); err == nil {
			// sed address is a regex, so dots in the username match any char
			re := regexp.MustCompile("^" + username + `\|`)
			dirRe := regexp.MustCompile(`/var/www/html/[^|]*`)
			lines := strings.SplitAfter(string(b), "\n")
			for i, l := range lines {
				if re.MatchString(l) {
					loc := dirRe.FindStringIndex(l)
					if loc != nil {
						lines[i] = l[:loc[0]] + p + l[loc[1]:]
					}
				}
			}
			os.WriteFile(list, []byte(strings.Join(lines, "")), 0o644)
		}
		fmt.Printf("Success: FTP path for user '%s' changed successfully.\n", username)
		return 0
	}
	if debug {
		fmt.Println("ERROR: Failed to change FTP path with usermod command:")
		fmt.Printf("podman exec openadmin_ftp usermod -d %s %s\n", cpath, username)
	} else {
		fmt.Printf("ERROR: Failed to change FTP path. To debug, run this command on terminal: opencli ftp-path %s %s %s --debug\n", username, p, opUser)
	}
	return 1
}

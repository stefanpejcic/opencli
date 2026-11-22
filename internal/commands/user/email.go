// ##############################################################################
// Script Name: internal/commands/user/email.go
// Description: Change email for user
// Usage: opencli user-email <USERNAME> <NEW_EMAIL>
// Author: Stefan Pejcic
// Created: 06.12.2023
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

package user

import (
	"fmt"
	"regexp"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/redis"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-email",
		Desc:  "Change email for user",
		Usage: "opencli user-email <USERNAME> <NEW_EMAIL>",
		Docs:  "https://docs.openpanel.com",
		Run:   email,
	})
}

var emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

func emailUsage() {
	fmt.Println("Usage: opencli user-email <USERNAME> <NEW_EMAIL>")
	fmt.Println("")
	fmt.Println("Updates the email address for a specified account.")
	fmt.Println("")
	fmt.Println("Arguments:")
	fmt.Println("  USERNAME   - The username of the user to update")
	fmt.Println("  NEW_EMAIL  - The new email address to assign")
	fmt.Println("")
	fmt.Println("Example:")
	fmt.Println("  opencli user-email john john.doe@newdomain.com")
}

func email(args []string) int {
	db.Require()
	if len(args) != 2 {
		emailUsage()
		return 1
	}
	username, newEmail := args[0], args[1]

	ok := emailUpdate(username, newEmail)
	if !ok {
		eprintln("Error: Failed to update email for user '" + username + "'")
		return 1
	}
	sentinelBg("--action=user_email", "--title=User account email changed", "--message=Email address for user account '"+username+"' has been changed to '"+newEmail+"'.")
	fmt.Printf("Success: Email for user '%s' updated to '%s'\n", username, newEmail)
	return 0
}

func emailUpdate(username, newEmail string) bool {
	esc := db.Escape(username)
	count, _ := db.Cmd("SELECT COUNT(*) FROM users WHERE username = '"+esc+"';", "-s", "-N").Out()
	if count != "1" {
		eprintln("Error: User '" + username + "' not found in database")
		return false
	}
	if !emailRe.MatchString(newEmail) {
		eprintln("Error: Invalid email format: " + newEmail)
		return false
	}
	// set -e kills the script when mariadb fails here
	if err := db.Exec("UPDATE users SET email = '" + db.Escape(newEmail) + "' WHERE username = '" + esc + "';"); err != nil {
		cli.Exit(1)
	}
	id, err := db.Cmd("SELECT id FROM users WHERE username = '"+esc+"';", "-N", "-s").Out()
	if err != nil {
		cli.Exit(1)
	}
	redis.DropUserCache(id, username)
	return true
}

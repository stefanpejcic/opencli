// ##############################################################################
// Script Name: internal/commands/user/2fa.go
// Description: Check or disable 2FA for a user.
// Usage: opencli user-2fa <username> [disable]
// Author: Stefan Pejcic
// Created: 16.11.2023
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

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/redis"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-2fa",
		Desc:  "Check or disable 2FA for a user.",
		Usage: "opencli user-2fa <username> [disable]",
		Run:   twofa,
	})
}

func twofa(args []string) int {
	if len(args) == 0 || len(args) > 2 {
		fmt.Println("Usage: opencli user-2fa <username> [disable]")
		return 1
	}
	username := args[0]
	action := cli.Arg(args, 1)
	db.Require()
	esc := db.Escape(username)

	if action == "disable" {
		db.Exec("UPDATE users SET twofa_enabled='0' WHERE username='" + esc + "';")
		id, _ := db.Cmd("SELECT id FROM users WHERE username='"+esc+"';", "-N", "-s").Out()
		redis.DropUserCache(id, username)
		fmt.Printf("Two-factor authentication for %s is now %sDISABLED%s.\n", username, colRed, colReset)
		return 0
	}
	v, _ := db.Cmd("SELECT twofa_enabled FROM users WHERE username='"+esc+"';", "-s").Out()
	switch v {
	case "0":
		fmt.Printf("Two-factor authentication for %s is %sDISABLED%s.\n", username, colRed, colReset)
	case "1":
		fmt.Printf("Two-factor authentication for %s is %sENABLED%s.\n", username, colGreen, colReset)
	default:
		fmt.Printf("%sInvalid twofa value for %s.%s\n", colRed, username, colReset)
	}
	return 0
}

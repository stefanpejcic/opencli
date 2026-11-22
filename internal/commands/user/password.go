// ##############################################################################
// Script Name: internal/commands/user/password.go
// Description: Reset password for a user.
// Usage: opencli user-password <USERNAME> <NEW_PASSWORD | random>
// Author: Stefan Pejcic
// Created: 30.11.2023
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
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/passwd"
	"opencli/internal/redis"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-password",
		Desc:  "Reset password for a user.",
		Usage: "opencli user-password <USERNAME> <NEW_PASSWORD | random>",
		Docs:  "https://docs.openpanel.com",
		Run:   password,
	})
}

func pwRandInt(n int) int {
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	return int(v.Int64())
}

func pwPick(pool string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = pool[pwRandInt(len(pool))]
	}
	return string(b)
}

// guarantees at least one upper, lower, digit and punctuation char (plus 8 fully random chars) so this always scores top of the password_strength rubric
func pwGenerate() string {
	pool := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()-_=+"
	pw := []byte(pwPick(pool, 8) + pwPick("ABCDEFGHIJKLMNOPQRSTUVWXYZ", 1) + pwPick("abcdefghijklmnopqrstuvwxyz", 1) + pwPick("0123456789", 1) + pwPick("!@#$%^&*()-_=+", 1))
	for i := len(pw) - 1; i > 0; i-- {
		j := pwRandInt(i + 1)
		pw[i], pw[j] = pw[j], pw[i]
	}
	return string(pw)
}

var pwDigits = regexp.MustCompile(`^[0-9]+$`)

func password(args []string) int {
	if len(args) != 2 {
		fmt.Println("Usage: opencli user-password <USERNAME> <NEW_PASSWORD | random>")
		return 1
	}
	username, newPassword := args[0], args[1]
	db.Require()

	// UPDATE on a missing user matches no rows and would still report success
	userID, err := db.Cmd("SELECT id FROM users WHERE username='"+db.Escape(username)+"' LIMIT 1;", "-N", "-s").Out()
	if err != nil {
		cli.Fail("Error: Could not look up user '%s' in the database.", username)
	}
	if !pwDigits.MatchString(userID) {
		cli.Fail("Error: User '%s' does not exist.", username)
	}

	random := false
	if newPassword == "random" {
		newPassword = pwGenerate()
		random = true
	}
	passwd.RequireStrength(newPassword)
	if !random {
		passwd.RequireNotCommon(newPassword)
	}
	salt := pwPick("0123456789abcdef", 16)
	hashed, _ := sh.C("openssl", "passwd", "-6", "-salt", salt, newPassword).Out()

	q := "UPDATE users SET password='" + db.Escape(hashed) + "' WHERE username='" + db.Escape(username) + "';"
	if db.Exec(q) != nil {
		cli.Fail("Error: Data insertion failed.")
	}
	scan, _ := redis.CLI("--scan", "--pattern", "session:"+userID+":*").Out()
	sessionCount := countLines(scan)
	redis.DropUserSessions(userID)
	// MCP tokens go too, API JWTs die on their own with the old password
	mcpOut, _ := db.Cmd("SELECT COUNT(*) FROM mcp_tokens WHERE user_id="+userID+"; DELETE FROM mcp_tokens WHERE user_id="+userID+";", "-N", "-s").NoErr().Out()
	mcp := strings.SplitN(mcpOut, "\n", 2)[0]
	if !pwDigits.MatchString(mcp) {
		mcp = "0"
	}
	sentinelBg("--action=user_password", "--title=User account password changed", fmt.Sprintf("--message=Password for user account '%s' has been changed. %d session(s) terminated, %s MCP token(s) revoked.", username, sessionCount, mcp))
	msg := "Successfully changed password for user " + username
	if random {
		msg += ", new random generated password is: " + newPassword
	}
	fmt.Println(msg)
	return 0
}

// ##############################################################################
// Script Name: internal/commands/useracct/rename.go
// Description: Rename username.
// Usage: opencli user-rename <old_username> <new_username>
// Author: Stefan Pejcic
// Created: 23.11.2023
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

package useracct

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/redis"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-rename",
		Desc:  "Rename username.",
		Usage: "opencli user-rename <old_username> <new_username>",
		Run:   rename,
	})
}

func rename(args []string) int {
	if len(args) < 2 || len(args) > 3 {
		fmt.Println("Usage: opencli user-rename <old_username> <new_username>")
		return 1
	}
	oldName, newName := args[0], args[1]
	renameContext := false
	for _, a := range args {
		if a == "--context" {
			renameContext = true
		}
	}
	if renameContext {
		fmt.Println("Error: --context is not supported, only the username is renamed and the context stays the same.")
		return 1
	}

	// validate username first
	if regexp.MustCompile(`[[:space:]]|[-_]`).MatchString(newName) || !regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(newName) ||
		len([]rune(newName)) < 3 || len([]rune(newName)) > 20 {
		fmt.Printf("Error: The username '%s' is not valid. Ensure it is a single word with no hyphens or underscores, contains only letters and numbers, and has a length between 3 and 20 characters.\n", newName)
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openpanel")
		return 1
	}
	for _, f := range conf.Lines(addForbiddenFile) {
		if strings.ToLower(newName) == strings.ToLower(strings.TrimSuffix(f, "\r")) {
			fmt.Printf("Error: The username '%s' is not allowed.\n", newName)
			fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#reserved-usernames")
			return 1
		}
	}

	// check in db
	db.Require()
	n, err := db.Cmd("SELECT COUNT(*) FROM users WHERE username = '"+db.Escape(newName)+"'", "-sN").Out()
	if err != nil {
		cli.Fail("Error: Unable to check username existence in the database.")
	}
	if text.Atoi(n) > 0 {
		cli.Fail("Error: Username '%s' already exists.", newName)
	}
	n, _ = db.Cmd("SELECT COUNT(*) FROM users WHERE server = '"+db.Escape(newName)+"'", "-sN").Out()
	if text.Atoi(n) > 0 {
		cli.Fail("Error: Context '%s' already exists.", newName)
	}

	// get context
	info, _ := db.Plain("SELECT id, server FROM users WHERE username = '"+db.Escape(oldName)+"';", "-s").Out()
	var ids, ctxs []string
	for _, l := range strings.Split(info, "\n") {
		ids = append(ids, text.Field(l, 1))
		ctxs = append(ctxs, text.Field(l, 2))
	}
	// echo "$id,$ctx" | cut -d, -f1 keeps every line
	userID := strings.Join(ids, "\n")
	context := strings.Join(ctxs, "\n")
	if userID == "" {
		cli.Fail("ERROR: user %s does not exist.", oldName)
	}
	uid := statUID("/home/" + context)
	if uid == "" || !isSocket("/hostfs/run/user/"+uid+"/podman/podman.sock") {
		cli.Fail("ERROR: Context '%s' not found.", context)
	}

	// rename in db, then core + stats dirs
	q := "UPDATE users SET username='" + db.Escape(newName) + "' WHERE username='" + db.Escape(oldName) + "';"
	if db.Cmd(q).Run() != nil {
		cli.Fail("Error: Changing username in database failed!")
	}
	renameMoveData(oldName, newName, context) // before anything reads the new paths
	redis.DropUserCache(userID, oldName, newName)

	// postfwd ratelimit rules use usernames
	sh.C("bash", "-c", sh.Self()+" email-ratelimit --delete-user="+oldName+" && "+sh.Self()+" email-ratelimit --username="+newName).Quiet().Background()
	sh.Opencli("sentinel", "--action=user_rename", "--title=User account username changed", "--message=Username for user account '"+oldName+"' has been changed to: '"+newName+"'.").Quiet().Background()

	fmt.Printf("User '%s' successfully renamed to '%s'.\n", oldName, newName)
	if context != newName {
		fmt.Printf("Files, containers and the system user stay under context '%s'.\n", context)
	}
	return 0
}

// only data keyed by username moves; /home, the system user, podman volumes, ftp and quotas are keyed by the context, which stays the same on rename
func renameMoveData(oldName, newName, context string) {
	core := conf.UsersDir
	for _, dir := range []string{core, "/var/log/caddy/stats"} {
		if !conf.IsDir(dir + "/" + oldName) {
			continue
		}
		if _, err := os.Lstat(dir + "/" + newName); err == nil {
			// renaming back to the context, its core dir is still there so merge into it
			if dir == core && newName == context && conf.IsDir(dir+"/"+newName) {
				if cpNoClobber(dir+"/"+oldName, dir+"/"+newName) == nil {
					os.RemoveAll(dir + "/" + oldName)
				}
				continue
			}
			fmt.Printf("WARNING: %s/%s already exists, keeping %s/%s\n", dir, newName, dir, oldName)
			continue
		}
		os.Rename(dir+"/"+oldName, dir+"/"+newName)
	}

	// admin.backups is looked up by context everywhere, so it stays in the context dir
	if context == newName {
		return
	}
	for _, f := range []string{"admin.backups"} {
		src := core + "/" + newName + "/" + f
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		os.MkdirAll(core+"/"+context, 0o777)
		dst := core + "/" + context + "/" + f
		if _, err := os.Lstat(dst); err != nil {
			os.Rename(src, dst)
		}
	}
}

// cpNoClobber is cp -a -n src/. dst/
func cpNoClobber(src, dst string) error {
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		s, d := src+"/"+e.Name(), dst+"/"+e.Name()
		st, err := os.Lstat(s)
		if err != nil {
			return err
		}
		if dt, err := os.Lstat(d); err == nil {
			if st.IsDir() && dt.IsDir() {
				if err := cpNoClobber(s, d); err != nil {
					return err
				}
			}
			continue
		}
		if err := copyTree(s, d, st, true); err != nil {
			return err
		}
	}
	return nil
}

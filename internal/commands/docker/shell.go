// ##############################################################################
// Script Name: internal/commands/docker/shell.go
// Description: Open an interactive shell inside a system or user container, picking the user and container via fzf if not given.
// Usage: opencli docker [<user> [<container>]]
// Author: Stefan Pejcic
// Created: 07.03.2025
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

package docker

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "docker",
		Desc:  "Open an interactive shell inside a system or user container, picking the user and container via fzf if not given.",
		Usage: "opencli docker [<user> [<container>]]",
		Docs:  "https://docs.openpanel.com",
		Run:   shell,
	})
}

func shellDie(msg string) {
	fmt.Fprintln(os.Stderr, "ERROR: "+msg)
	cli.Exit(1)
}

func shellContext(input string) string {
	if input == "" {
		return "default"
	}
	username := input
	if i := strings.LastIndex(input, "_"); i >= 0 {
		username = input[i+1:]
	}
	esc := strings.ReplaceAll(username, "'", "''")
	ctx, err := db.Plain("SELECT server FROM users WHERE username = '"+esc+"' OR (username REGEXP '^SUSPENDED_[0-9]{14}_' AND SUBSTRING(username, 26) = '"+esc+"') ORDER BY username = '"+esc+"' DESC LIMIT 1;", "-N", "-s").NoErr().Out()
	if err != nil {
		// set -e in the script ended it here
		cli.Exit(sh.Code(err))
	}
	if ctx == "" {
		shellDie("No podman context found for username: " + username)
	}
	return ctx
}

func shellFzf(list, prompt, header string) (string, bool) {
	out, err := sh.C("fzf", "--prompt="+prompt+" > ", "--header="+header, "--layout=reverse", "--border").Stdin(list).Out()
	return out, err == nil
}

func shellPickContainer(ctx, prompt string) string {
	list, err := podman.Ctx(ctx, "ps", "--format", `{{.Names}}\t{{.Image}}\t{{.Status}}`).NoErr().Out()
	sel, ok := shellFzf(list, prompt, "Select a container (ESC to quit)")
	if err != nil || !ok {
		shellDie("No container selected")
	}
	if f := strings.Fields(sel); len(f) > 0 {
		return f[0]
	}
	return ""
}

func shellPickUser() string {
	out, _ := db.Plain("SELECT username FROM users ORDER BY username;", "-s", "-N").NoErr().Out()
	re := regexp.MustCompile(`^.*_`)
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		lines = append(lines, re.ReplaceAllString(l, ""))
	}
	sel, ok := shellFzf(strings.Join(lines, "\n")+"\n", "user", "Select a user (ESC to quit)")
	if !ok {
		shellDie("No user selected")
	}
	return sel
}

func shellExec(ctx, container string) int {
	sel, err := podman.Ctx(ctx, "exec", container, "sh", "-c", "command -v bash || command -v sh").NoErr().Out()
	if err != nil {
		return sh.Code(err)
	}
	return podman.Ctx(ctx, "exec", "-it", container, sel).StdinReader(os.Stdin).Code()
}

func shell(args []string) int {
	switch len(args) {
	case 0:
		requirement.Command("fzf")
		user := shellPickUser()
		ctx := shellContext(user)
		return shellExec(ctx, shellPickContainer(ctx, user))
	case 1:
		requirement.Command("fzf")
		ctx := shellContext(args[0])
		return shellExec(ctx, shellPickContainer(ctx, args[0]))
	case 2:
		return shellExec(shellContext(args[0]), args[1])
	}
	fmt.Fprintln(os.Stderr, "Usage: opencli docker [<user> [<container>]]")
	return 1
}

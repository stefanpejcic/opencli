// ##############################################################################
// Script Name: internal/commands/general/podman.go
// Description: Run podman or podman-compose against a user's rootless podman, replaces podman_user and podman_compose_user from lib/podman.sh.
// Usage: opencli podman <USERNAME> [compose] <ARGS...> | opencli podman <USERNAME> --socket
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
	"fmt"
	"os"

	"opencli/internal/cli"
	"opencli/internal/podman"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "podman",
		Desc:  "Run podman or podman-compose against a user's rootless podman, replaces podman_user and podman_compose_user from lib/podman.sh.",
		Usage: "opencli podman <USERNAME> [compose] <ARGS...> | opencli podman <USERNAME> --socket",
		Run:   podmanCmd,
	})
}

func podmanCmd(args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: opencli podman <USERNAME> [compose] <ARGS...> | opencli podman <USERNAME> --socket")
		return 1
	}
	user, rest := args[0], args[1:]
	if len(rest) == 1 && rest[0] == "--socket" {
		sock, err := podman.Socket(user)
		if err != nil {
			return 1
		}
		fmt.Println(sock)
		return 0
	}
	if _, err := podman.Socket(user); err != nil {
		return 1
	}
	c := podman.User(user, rest...)
	// compose runs in the current dir like podman_compose_user, so cd /home/USER first
	if len(rest) > 0 && rest[0] == "compose" {
		c = podman.ComposeUser(user, rest[1:]...)
	}
	return c.StdinReader(os.Stdin).Code()
}

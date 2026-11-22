// ##############################################################################
// Script Name: internal/commands/domainsadd/bg.go
// Description: Internal helper for domains-add, runs the steps that bash detached with nohup.
// Usage: opencli domains-add-bg <compose|reload|restart|ensure> ARGS...
// Author: Stefan Pejcic
// Created: 20.08.2024
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

package domainsadd

import (
	"os"

	"opencli/internal/cli"
	"opencli/internal/podman"
)

// hidden helper, the bash scripts detached these steps with nohup so they outlive the command
func init() {
	cli.Register(&cli.Command{
		Name:   "domains-add-bg",
		Desc:   "Internal helper for domains-add.",
		Usage:  "opencli domains-add-bg <compose|reload|restart|ensure> ARGS...",
		Hidden: true,
		Run:    bgRun,
	})
}

func bgRun(args []string) int {
	switch cli.Arg(args, 0) {
	case "compose":
		ctx := cli.Arg(args, 1)
		os.Chdir("/home/" + ctx + "/")
		podman.ComposeUserRetry(ctx, true, append([]string{"-f", "/home/" + ctx + "/docker-compose.yml"}, args[2:]...)...)
	case "reload":
		ctx, ws := cli.Arg(args, 1), cli.Arg(args, 2)
		podman.ReloadWebserver(ctx, ws, args[3:]...)
		podman.SyncVarnish(ctx)
	case "restart":
		podman.RestartWebserver(cli.Arg(args, 1), cli.Arg(args, 2))
	case "ensure":
		podman.EnsureRunning(cli.Arg(args, 1), cli.Arg(args, 2), cli.Arg(args, 3), 0)
	}
	return 0
}

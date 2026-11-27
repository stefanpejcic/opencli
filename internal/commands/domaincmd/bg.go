// ##############################################################################
// Script Name: internal/commands/domaincmd/bg.go
// Description: Internal helper for domain.
// Usage: opencli domain-bg <cert-wait|mail-restart> ARGS...
// Author: Stefan Pejcic
// Created: 09.02.2025
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

package domaincmd

import (
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	wm "opencli/internal/webmail"
)

// the bash script detached these steps with nohup so they outlive the command
func bgRun(args []string) int {
	switch cli.Arg(args, 0) {
	case "cert-wait":
		host, crt, key := cli.Arg(args, 1), cli.Arg(args, 2), cli.Arg(args, 3)
		for i := 0; i < 60; i++ {
			time.Sleep(10 * time.Second)
			if conf.IsFile(crt) && conf.IsFile(key) {
				sh.Opencli("domain", "set", host, "--no-restart").Run()
				return 0
			}
		}
	case "mail-restart":
		dir := "/usr/local/mail/openmail/"
		if conf.IsDir(dir) && sh.C("podman-compose", "down").Dir(dir).OK() {
			sh.C("podman-compose", "up", "-d", "mailserver", wm.Service()).Dir(dir).Run()
		}
	}
	return 0
}

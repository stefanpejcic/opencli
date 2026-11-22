// ##############################################################################
// Script Name: internal/commands/email/manage.go
// Description: Pass commands through to docker-mailserver's setup CLI inside the mailserver container.
// Usage: opencli email-manage <COMMAND> [<ARGS>...]
// Author: Stefan Pejcic
// Created: 31.08.2024
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

package email

import (
	"fmt"

	"opencli/internal/cli"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "email-manage",
		Desc:  "Pass commands through to docker-mailserver's setup CLI inside the mailserver container.",
		Usage: "opencli email-manage <COMMAND> [<ARGS>...]",
		Run:   manage,
	})
}

func manage(args []string) int {
	if len(args) < 1 {
		fmt.Println("Usage: opencli email-manage <command> [<args>...]")
		return 1
	}
	emailGate()
	return sh.Code(sh.C("podman", append([]string{"exec", container}, args...)...).Run())
}

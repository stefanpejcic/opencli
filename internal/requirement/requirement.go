// ##############################################################################
// Script Name: internal/requirement/requirement.go
// Description: Installs a missing CLI tool through the system package manager.
// Usage: imported by command packages
// Author: Stefan Pejcic
// Created: 20.11.2026
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

package requirement

import (
	"fmt"

	"opencli/internal/cli"
	"opencli/internal/sh"
)

// Command installs cmd if missing, pkg defaults to cmd, exits like the bash helper when that fails
func Command(cmd string, pkg ...string) {
	p := cmd
	if len(pkg) > 0 && pkg[0] != "" {
		p = pkg[0]
	}
	if sh.Has(cmd) {
		return
	}
	switch {
	case sh.Has("apt-get"):
		// waits for the dpkg lock instead of failing when another install runs at the same time
		sh.Quiet("apt-get", "-o", "DPkg::Lock::Timeout=300", "update", "-qq")
		sh.Quiet("apt-get", "-o", "DPkg::Lock::Timeout=300", "install", "-y", "-qq", p)
	case sh.Has("dnf"):
		if sh.Quiet("dnf", "install", "-y", "-q", p) != nil {
			sh.Quiet("dnf", "install", "-y", "-q", "epel-release")
			sh.Quiet("dnf", "install", "-y", "-q", p)
		}
	case sh.Has("yum"):
		if sh.Quiet("yum", "install", "-y", "-q", p) != nil {
			sh.Quiet("yum", "install", "-y", "-q", "epel-release")
			sh.Quiet("yum", "install", "-y", "-q", p)
		}
	case sh.Has("pacman"):
		sh.Quiet("pacman", "-Sy", "--noconfirm", p)
	default:
		fmt.Printf("Error: No compatible package manager found. Please install %s manually and try again.\n", p)
		cli.Exit(1)
	}
	if !sh.Has(cmd) {
		fmt.Printf("Error: %s installation failed. Please install %s manually and try again.\n", p, p)
		cli.Exit(1)
	}
}

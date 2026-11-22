// ##############################################################################
// Script Name: internal/commands/general/version.go
// Description: Displays the current (installed) version of OpenPanel container image.
// Usage: opencli version
// Author: Stefan Pejcic
// Created: 15.11.2023
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
	"regexp"
	"sort"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "version",
		Desc:  "Displays the current (installed) version of OpenPanel container image.",
		Usage: "opencli version",
		Run:   version,
	})
}

var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func version(args []string) int {
	v := ""
	// used on terminal
	if conf.IsFile(conf.RootEnv) {
		var vals []string
		re := regexp.MustCompile(`^VERSION="([^"]+)"$`)
		for _, l := range conf.Lines(conf.RootEnv) {
			if strings.HasPrefix(l, "VERSION=") {
				vals = append(vals, re.ReplaceAllString(l, "$1"))
			}
		}
		v = text.Xargs(strings.Join(vals, "\n"))
	}
	// used inside openpanel ui
	if v == "" {
		var tags []string
		for _, t := range strings.Split(sh.C("podman", "images", "--format", "{{.Tag}}", "openpanel/openpanel").Str(), "\n") {
			if semver.MatchString(t) {
				tags = append(tags, t)
			}
		}
		if len(tags) > 0 {
			sort.Slice(tags, func(i, j int) bool { return text.VersionLess(tags[i], tags[j]) })
			v = tags[len(tags)-1]
		}
	}
	if v == "" {
		fmt.Fprintln(os.Stderr, `{"error": "Podman image or .env files are missing."}`)
		return 1
	}
	fmt.Println(v)
	return 0
}

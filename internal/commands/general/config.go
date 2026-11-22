// ##############################################################################
// Script Name: internal/commands/general/config.go
// Description: Get or update a value in the OpenPanel configuration file.
// Usage: opencli config <get|update> <setting_name> [new_value]
// Author: Stefan Pejcic
// Created: 01.11.2023
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
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/redis"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "config",
		Desc:  "Get or update a value in the OpenPanel configuration file.",
		Usage: "opencli config <get|update> <setting_name> [new_value]",
		Run:   config,
	})
}

func config(args []string) int {
	if len(args) < 2 {
		fmt.Println("Usage: opencli config [get|update] <parameter_name> [new_value]")
		return 1
	}
	command, param := args[0], args[1]
	switch command {
	case "get":
		configRead(param)
	case "update":
		if len(args) != 3 {
			fmt.Printf("ERROR: New value is not provided for the parameter '%s'.\n\n", param)
			fmt.Println("Usage: opencli config update <parameter_name> <new_value>")
			fmt.Println("See the list of available parameters with examples here: https://openpanel.com/docs/opencli/config/#update")
			return 1
		}
		value := args[2]
		configUpdate(param, value)
		if param == "email" {
			// CSF takes one address so the first one from a comma list goes there
			first, _, _ := strings.Cut(value, ",")
			first = text.NoSpace(first)
			if regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`).MatchString(first) {
				csf := "/etc/csf/csf.conf"
				if b := conf.Read(csf); b != "" || conf.IsFile(csf) {
					conf.WriteFileKeep(csf, strings.Replace(b, `LF_ALERT_TO = ""`, `LF_ALERT_TO = "`+first+`"`, -1), true)
				}
			}
		}
	default:
		fmt.Print("ERROR: Invalid command, only 'get' and 'update' are allowed.\n\n")
		fmt.Println("Usage: opencli config [get|update] <parameter_name> [new_value]")
		fmt.Println("See the list of available commands here: https://openpanel.com/docs/opencli/config/")
		return 1
	}
	return 0
}

// keyRe treats the name like grep did, as a regex anchored at line start
func keyRe(param string) *regexp.Regexp {
	re, err := regexp.Compile("^" + param + "=")
	if err != nil {
		re = regexp.MustCompile("^" + regexp.QuoteMeta(param) + "=")
	}
	return re
}

func configRead(param string) {
	re := keyRe(param)
	var vals []string
	exists := false
	for _, l := range conf.Lines(conf.Panel) {
		if loc := re.FindStringIndex(l); loc != nil {
			exists = true
			vals = append(vals, l[loc[1]:])
		}
	}
	val := strings.TrimRight(strings.Join(vals, "\n"), "\n")
	switch {
	case val != "":
		fmt.Println(val)
	case exists:
		fmt.Printf("Parameter %s has no value.\n", param)
	default:
		fmt.Printf("Parameter %s does not exist.\n", param)
	}
}

func configUpdate(param, value string) {
	re := keyRe(param)
	lines := conf.Lines(conf.Panel)
	match := func(pattern string) bool {
		r := regexp.MustCompile(pattern)
		for _, l := range lines {
			if r.MatchString(l) {
				return true
			}
		}
		return false
	}
	exists := false
	for _, l := range lines {
		if re.MatchString(l) {
			exists = true
		}
	}
	if !exists {
		fmt.Printf("ERROR: Parameter '%s' was not found in the configuration file.\n\n", param)
		fmt.Println("Usage: opencli config update <parameter_name> <new_value>")
		fmt.Println("See the list of available parameters here: https://openpanel.com/docs/opencli/config/#available-options")
		cli.Exit(1)
	}
	p := re.String()[1 : len(re.String())-1]
	var edit *regexp.Regexp
	var repl string
	switch {
	case match(`^` + p + `=".*"`):
		// quoted stays quoted
		edit, repl = regexp.MustCompile(`^(`+p+`=)".*"`), `\1"`+value+`"`
	case match(`^` + p + `=[^"].*`):
		edit, repl = regexp.MustCompile(`^(`+p+`=).*`), `\1`+value
	default:
		edit, repl = regexp.MustCompile(`^(`+p+`=)$`), `\1`+value
	}
	for i, l := range lines {
		if m := edit.FindStringSubmatch(l); m != nil {
			lines[i] = text.SedRepl(repl, m) + l[len(m[0]):]
		}
	}
	out := strings.Join(lines, "\n")
	if strings.HasSuffix(conf.Read(conf.Panel), "\n") {
		out += "\n"
	}
	conf.WriteFileKeep(conf.Panel, out, false)
	fmt.Printf("Updated %s to %s\n", param, value)

	// the panel caches these for hours, drop them so the change shows up right away
	switch param {
	case "temporary_links":
		redis.DropKey("openpanel_cache_temporary_links_setting")
	case "how_to_guides":
		redis.DropKey("openpanel_cache_how_to_links_for_dashboard_page")
	}
	switch param {
	case "email", "autoupdate", "autopatch", "key", "default_php_version", "max_cpu", "max_ram":
	default:
		sh.C("podman", "restart", "openpanel").Quiet().Background()
	}
}

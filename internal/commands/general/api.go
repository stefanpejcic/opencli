// ##############################################################################
// Script Name: internal/commands/general/api.go
// Description: Check status, enable or disable OpenAdmin API access, and list available API endpoints.
// Usage: opencli api <status|on|off|list>
// Author: Stefan Pejcic
// Created: 04.09.2024
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
	"path/filepath"
	"regexp"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "api",
		Desc:  "Check status, enable or disable OpenAdmin API access, and list available API endpoints.",
		Usage: "opencli api <status|on|off|list>",
		Run:   api,
	})
}

const enterpriseLink = "https://my.openpanel.com/index.php?rp=/store/openpanel/enterprise-license"

func apiUsage() {
	fmt.Print(`
Usage: opencli api {status|on|off|list}
Docs:  https://openpanel.com/docs/admin/api/

Examples:
  opencli api status              Display current OpenAdmin API status.
  opencli api on                  Enable OpenAdmin API access.
  opencli api off                 Disable OpenAdmin API access.
  opencli api list                List available API endpoints with curl examples.

`)
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && (st.IsDir() || st.Mode().Perm()&0o111 != 0)
}

var apiExecRe = regexp.MustCompile(`^ExecStart=(\S+)`)
var apiURLRe = regexp.MustCompile(`https?://[^ ]+`)

// the list is built into the openadmin binary, from the same file as the swagger docs on Settings > API
func apiListEndpoints() int {
	var bins []string
	for _, l := range conf.Lines("/etc/openpanel/openadmin/service/openadmin.service") {
		if m := apiExecRe.FindStringSubmatch(l); m != nil {
			bins = append(bins, m[1])
		}
	}
	bin := strings.Join(bins, "\n")
	// old installs still have gunicorn there
	if !strings.HasPrefix(bin, "/usr/local/admin/openadmin-") || !isExec(bin) {
		bin = ""
		if m, _ := filepath.Glob("/usr/local/admin/openadmin-*"); len(m) > 0 {
			bin = m[0]
		}
	}
	if !isExec(bin) {
		fmt.Println("ERROR: OpenAdmin binary not found in /usr/local/admin/")
		cli.Exit(1)
	}
	url := ""
	for _, l := range strings.Split(sh.Opencli("admin").NoErr().Str(), "\n") {
		if m := apiURLRe.FindString(l); m != "" {
			url = m
			break
		}
	}
	if url == "" {
		url = "https://localhost:2087/"
	}
	if sh.C(bin, "api-endpoints", strings.TrimSuffix(url, "/")).Run() != nil {
		fmt.Println("ERROR: This OpenAdmin version can't list endpoints, update it with: opencli update --admin")
		cli.Exit(1)
	}
	return 0
}

func api(args []string) int {
	if cli.Arg(args, 0) == "" {
		apiUsage()
		return 1
	}
	if grepCut(conf.Panel, "key") == "" {
		fmt.Println("Error: OpenPanel Community edition does not support API access. Please consider purchasing the Enterprise version that has remote API access and integrations with billing softwares such as WHMCS and FOSSBilling.")
		fmt.Println(enterpriseLink)
		return 1
	}
	switch args[0] {
	case "status":
		return sh.Opencli("config", "get", "api").Code()
	case "on", "off":
		sh.Opencli("config", "update", "api", args[0]).Run()
		sh.Opencli("sentinel", "--action=admin_api", "--title=OpenAdmin API is "+args[0], "--message=API access for the administrator-level panel is now: '"+args[0]+"'.").Quiet().Background()
		if sh.C("systemctl", "is-active", "--quiet", "admin").OK() {
			return sh.C("systemctl", "restart", "admin").ErrToOut().Code()
		}
		return 0
	case "list":
		return apiListEndpoints()
	}
	fmt.Println("Invalid option: " + args[0])
	apiUsage()
	return 1
}

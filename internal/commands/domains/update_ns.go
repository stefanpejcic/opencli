// ##############################################################################
// Script Name: internal/commands/domains/update_ns.go
// Description: Change nameservers for a single or all dns zones.
// Usage: opencli domains-update_ns <DOMAIN_NAME | --all> [-y]
// Author: Stefan Pejcic
// Created: 20.08.2023
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

package domains

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-update_ns",
		Desc:  "Change nameservers for a single or all dns zones.",
		Usage: "opencli domains-update_ns <DOMAIN_NAME | --all> [-y]",
		Run:   updateNS,
	})
}

const nsZoneDir = "/etc/bind/zones"

var nsRecordRe = regexp.MustCompile(`^[^;].*IN[[:space:]]*NS[[:space:]]*`)

// cutField2 is grep '^key=' | cut -d= -f2
func cutField2(file, key string) string {
	var res []string
	for _, l := range conf.Lines(file) {
		if strings.HasPrefix(l, key+"=") {
			f := strings.Split(l, "=")
			res = append(res, f[1])
		}
	}
	return strings.Join(res, "\n")
}

func nsUpdateZone(zoneFile string, nameservers []string) {
	backupDir := nsZoneDir + "/backups"
	os.MkdirAll(backupDir, 0o755)
	content, err := os.ReadFile(zoneFile)
	if err == nil {
		os.WriteFile(backupDir+"/"+filepath.Base(zoneFile)+".bak", content, 0o644)
	}

	var b strings.Builder
	s := strings.TrimSuffix(string(content), "\n")
	if len(content) > 0 {
		for _, l := range strings.Split(s, "\n") {
			if !nsRecordRe.MatchString(l) {
				b.WriteString(l + "\n")
			}
		}
	}
	for _, ns := range nameservers {
		b.WriteString("@ IN NS " + ns + ".\n")
	}
	conf.WriteInPlace(zoneFile, b.String())
}

func updateNS(args []string) int {
	const configFile = "/etc/openpanel/openpanel/conf/openpanel.config"
	if !conf.IsFile(configFile) {
		fmt.Println("Configuration file not found: " + configFile)
		return 1
	}
	ns1, ns2 := cutField2(configFile, "ns1"), cutField2(configFile, "ns2")
	ns3, ns4 := cutField2(configFile, "ns3"), cutField2(configFile, "ns4")
	if ns1 == "" || ns2 == "" {
		fmt.Println("ns1 and ns2 must be set in the configuration file.")
		return 1
	}
	nameservers := []string{ns1, ns2}
	if ns3 != "" {
		nameservers = append(nameservers, ns3)
	}
	if ns4 != "" {
		nameservers = append(nameservers, ns4)
	}

	switch a := cli.Arg(args, 0); a {
	case "--all":
		if cli.Arg(args, 1) != "-y" {
			fmt.Println("You are about to update all zone files. This action cannot be undone.")
			fmt.Println("Proceed? (y/n)")
			ch := make(chan string, 1)
			go func() {
				l, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				ch <- strings.TrimSuffix(l, "\n")
			}()
			confirm := ""
			select {
			case confirm = <-ch:
			case <-time.After(10 * time.Second):
			}
			if confirm != "y" {
				fmt.Println("Operation aborted.")
				return 1
			}
		}
		zones, _ := filepath.Glob(nsZoneDir + "/*.zone")
		for _, z := range zones {
			if conf.IsFile(z) {
				nsUpdateZone(z, nameservers)
			}
		}
	case "":
		fmt.Println("Usage: /usr/local/opencli/domains/update_ns.sh [--all | --all -y | domain_name]")
		fmt.Println("Options:")
		fmt.Println("  --all       Update all zone files.")
		fmt.Println("  --all -y    Update all zone files without confirmation.")
		fmt.Println("  domain_name Update the zone file for the specified domain.")
		return 1
	default:
		zone := nsZoneDir + "/" + a + ".zone"
		if !conf.IsFile(zone) {
			fmt.Printf("Zone file for %s not found: %s\n", a, zone)
			return 1
		}
		nsUpdateZone(zone, nameservers)
	}

	// rndc reload rereads the changed zone files, reconfig only picks up new zones; bind9 only gets started when it isn't running
	if !sh.C("podman", "exec", "openpanel_dns", "rndc", "reload").Quiet().OK() {
		sh.C("podman-compose", "up", "-d", "bind9").Dir("/root").Quiet().Run()
	}

	fmt.Println("Nameservers have been updated and BIND9 zones reloaded.")
	return 0
}

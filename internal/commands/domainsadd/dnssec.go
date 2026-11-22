// ##############################################################################
// Script Name: internal/commands/domainsadd/dnssec.go
// Description: Enable DNSSEC for a domain, re-sign the zone after changes, or check its DNSSEC status.
// Usage: opencli domains-dnssec <DOMAIN> [--update | --check]
// Author: Stefan Pejcic
// Created: 09.07.2024
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
	"crypto/rand"
	"crypto/sha1"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "domains-dnssec",
		Desc:  "Enable DNSSEC for a domain, re-sign the zone after changes, or check its DNSSEC status.",
		Usage: "opencli domains-dnssec <DOMAIN> [--update | --check]",
		Run:   dnssec,
	})
}

const dnssecZoneDir = "/etc/bind/zones"

func dnssecFail(msg string) {
	fmt.Println("Error: " + msg)
	cli.Exit(1)
}

// the salt is the first 16 hex chars of sha1 over 1000 random bytes
func dnssecSalt() string {
	b := make([]byte, 1000)
	rand.Read(b)
	return fmt.Sprintf("%x", sha1.Sum(b))[:16]
}

func dnssecSign(zone, zoneFile string) {
	script := "cd " + dnssecZoneDir + " && dnssec-signzone -A -3 " + dnssecSalt() + " -N INCREMENT -P -o " + zone + " -t " + zoneFile + " >/dev/null 2>&1"
	if sh.C("podman", "exec", "openpanel_dns", "bash", "-c", script).Run() != nil {
		dnssecFail("Failed to sign the zone file")
	}
}

func dnssecReload(zone string) {
	if sh.Opencli("domains-dns", "reload", zone).Quiet().Run() != nil {
		dnssecFail("Failed to reload the DNS zone")
	}
}

func dnssec(args []string) int {
	zone := cli.Arg(args, 0)
	zoneFile := "/etc/bind/zones/" + zone + ".zone"
	if zone == "" {
		dnssecFail("Usage: opencli domains-dnssec <DOMAIN> [--update | --check]")
	}
	switch cli.Arg(args, 1) {
	case "--update":
		dnssecSign(zone, zoneFile)
		dnssecReload(zone)
		fmt.Println("Zone " + zone + " has been re-signed and DNS service reloaded.")
	case "--check":
		b, err := os.ReadFile("dsset-" + zone + ".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cat: dsset-%s.: No such file or directory\n", zone)
			dnssecFail("Domain " + zone + " has no DNSSEC enabled.")
		}
		os.Stdout.Write(b)
	default:
		dnssecSetup(zone, zoneFile)
	}
	return 0
}

func dnssecSetup(zone, zoneFile string) {
	if !conf.IsFile(zoneFile) {
		dnssecFail("Zone file " + zoneFile + " does not exist")
	}
	if os.Chdir(dnssecZoneDir) != nil {
		dnssecFail("Failed to change directory to " + dnssecZoneDir)
	}
	exec := func(script, errMsg string) {
		if sh.C("podman", "exec", "openpanel_dns", "bash", "-c", script).Run() != nil {
			dnssecFail(errMsg)
		}
	}
	// generate key pairs
	exec("dnssec-keygen -a NSEC3RSASHA1 -b 2048 -n ZONE "+zone+" >/dev/null 2>&1", "Failed to generate 2048-bit key")
	exec("dnssec-keygen -a NSEC3RSASHA1 -b 4096 -n ZONE "+zone+" >/dev/null 2>&1", "Failed to generate 4096-bit key")
	// allow bind group to read the keys
	exec("chgrp bind K"+zone+".* >/dev/null 2>&1", "Failed to change group of key files")
	exec("chmod g=r,o= K"+zone+".* >/dev/null 2>&1", "Failed to set permissions on key files")

	// include keys to the zone file
	pre := "K" + zone + "."
	var keys []string
	ents, _ := os.ReadDir(".")
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, pre) && strings.HasSuffix(n, ".key") && len(n) >= len(pre)+len(".key") {
			keys = append(keys, n)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		keys = []string{"K" + zone + ".*.key"}
	}
	for _, k := range keys {
		appendFile(zoneFile, "$INCLUDE "+k+"\n")
	}

	dnssecSign(zone, zoneFile)

	// point named.conf at the signed zone file instead
	if !dnssecPointSigned(zone) {
		dnssecFail("Failed to update the config file")
	}
	dnssecReload(zone)
	b, err := os.ReadFile("dsset-" + zone + ".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cat: dsset-%s.: No such file or directory\n", zone)
		dnssecFail("Failed to display DS records")
	}
	os.Stdout.Write(b)
}

// sed -i "/zone \"Z\"/,/file/s|\(file \"/etc/bind/zones/Z\.zone\)|\1.signed|" named.conf.local
func dnssecPointSigned(zone string) bool {
	path := addNamedConfLocal
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	z := sedRegexLiteral(zone)
	start, err1 := regexp.Compile(`zone "` + z + `"`)
	sub, err2 := regexp.Compile(`(file "/etc/bind/zones/` + z + `\.zone)`)
	if err1 != nil || err2 != nil {
		return false
	}
	s := string(b)
	trail := strings.HasSuffix(s, "\n")
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	in := false
	for i, l := range lines {
		apply := false
		if !in {
			if start.MatchString(l) {
				in, apply = true, true
			}
		} else {
			apply = true
			if strings.Contains(l, "file") {
				in = false
			}
		}
		if apply {
			if loc := sub.FindStringSubmatchIndex(l); loc != nil {
				lines[i] = l[:loc[1]] + ".signed" + l[loc[1]:]
			}
		}
	}
	out := strings.Join(lines, "\n")
	if trail {
		out += "\n"
	}
	return conf.WriteFileKeep(path, out, false) == nil
}

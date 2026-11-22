// ##############################################################################
// Script Name: internal/commands/docker/autostart.go
// Description: Prefetch (pull) container images for services listed as autostart, skipping if free disk space is low.
// Usage: opencli docker-autostart [-f|--force]
// Author: Stefan Pejcic
// Created: 14.05.2026
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

package docker

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "docker-autostart",
		Desc:  "Prefetch (pull) container images for services listed as autostart, skipping if free disk space is low.",
		Usage: "opencli docker-autostart [-f|--force]",
		Run:   autostart,
	})
}

const (
	autoComposeDir  = "/etc/openpanel/docker/compose/1.0"
	autoComposeFile = autoComposeDir + "/docker-compose.yml"
	autoFile        = autoComposeDir + "/autostart.services"
	// own runroot for pulls into the shared store: root's default one makes a pull unmount every running root container's layer, skopeo since podman 4.9 refuses another runroot (see docker/update.sh)
	autoRunroot = "/run/containers/shared-storage"
)

var (
	autoServices  = regexp.MustCompile(`^services:[ \t\n\v\f\r]*$`)
	autoSkipBlock = regexp.MustCompile(`^(networks|volumes|configs|secrets):[ \t\n\v\f\r]*$`)
	autoSvcHeader = regexp.MustCompile(`^  [A-Za-z0-9._-]+:[ \t\n\v\f\r]*$`)
	autoImageLine = regexp.MustCompile(`.*image:[ \t\n\v\f\r]*`)
	autoImageWS   = regexp.MustCompile(`image:[ \t\n\v\f\r]`)
	autoQuotes    = strings.NewReplacer(`"`, "", `'`, "")
	autoVarDef    = regexp.MustCompile(`\$\{[A-Za-z0-9_]+:-([^}]*)\}`)
	autoComment   = regexp.MustCompile(`#.*`)
)

// autoParse emits "service image" pairs, tracking 2-space service headers, off inside top-level networks/volumes/configs/secrets blocks
func autoParse(text string) []string {
	s := true
	svc := ""
	var pairs []string
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		switch {
		case autoServices.MatchString(l):
			s = true
			continue
		case autoSkipBlock.MatchString(l):
			s = false
			continue
		case s && autoSvcHeader.MatchString(l):
			svc = strings.TrimSuffix(strings.Fields(l)[0], ":")
			continue
		}
		if s && autoImageWS.MatchString(l) {
			line := autoImageLine.ReplaceAllString(l, "")
			line = autoQuotes.Replace(line)
			if i := strings.IndexAny(line, " \t\n\v\f\r"); i >= 0 {
				line = line[:i]
			}
			if svc != "" {
				pairs = append(pairs, svc+" "+line)
			}
		}
	}
	return pairs
}

func autostart(args []string) int {
	store := podman.SharedImageStore()
	force := cli.Arg(args, 0) == "-f" || cli.Arg(args, 0) == "--force"
	var st syscall.Statfs_t
	syscall.Statfs("/", &st)
	diskMB := int64(st.Bavail) * int64(st.Frsize) / 1024 / 1024
	switch {
	case force:
		fmt.Printf("Force flag set — skipping disk check (free: %dGB).\n", diskMB/1024)
	case diskMB <= 81920:
		fmt.Printf("Root disk free is %dGB (<=80GB) — skipping prefetch.\n", diskMB/1024)
		fmt.Println("Re-run with -f or --force to prefetch anyway.")
		return 0
	default:
		fmt.Printf("Root disk free is %dGB — proceeding with prefetch.\n", diskMB/1024)
	}
	if _, err := os.Stat(autoComposeFile); err != nil {
		fmt.Println("compose file not found: " + autoComposeFile)
		return 1
	}
	if _, err := os.Stat(autoFile); err != nil {
		fmt.Println("autostart file not found: " + autoFile)
		return 1
	}
	os.MkdirAll(store, 0o755)
	requirement.Command("skopeo")
	if os.Chdir(autoComposeDir) != nil {
		return 1
	}

	// primary: podman-compose config (resolves ${VAR} from adjacent .env)
	cfg, _ := sh.C("podman-compose", "-f", autoComposeFile, "config").NoErr().Raw()
	pairs := autoParse(cfg)
	// fallback: raw file with ${VAR:-default} -> default (may give compose-default tags)
	if len(pairs) == 0 {
		fmt.Println("compose config produced nothing — falling back to raw parse (tags may be compose defaults).")
		raw, _ := os.ReadFile(autoComposeFile)
		var lines []string
		for _, l := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
			lines = append(lines, autoVarDef.ReplaceAllString(l, "$1"))
		}
		pairs = autoParse(strings.Join(lines, "\n"))
	}
	svcImage := map[string]string{}
	for _, p := range pairs {
		svc, img := p, p
		if i := strings.Index(p, " "); i >= 0 {
			svc, img = p[:i], p[i+1:]
		}
		if svc == "" || img == "" || strings.Contains(img, "${") {
			continue
		}
		svcImage[svc] = img
	}
	// autostart service set (strip comments/blanks/whitespace), a last line without newline is not read
	var autoKeys []string
	auto := map[string]bool{}
	data, _ := os.ReadFile(autoFile)
	if i := strings.LastIndex(string(data), "\n"); i >= 0 {
		for _, l := range strings.Split(string(data)[:i], "\n") {
			l = strings.Join(strings.Fields(autoComment.ReplaceAllString(l, "")), "")
			if l != "" && !auto[l] {
				auto[l] = true
				autoKeys = append(autoKeys, l)
			}
		}
	}
	autoKeys = bashAssocOrder(autoKeys)
	fmt.Println()
	fmt.Println("Autostart services: " + strings.Join(autoKeys, " "))
	fmt.Printf("Compose services with images: %d\n", len(svcImage))
	fmt.Println()

	var sorted []string
	for s := range svcImage {
		sorted = append(sorted, s)
	}
	sort.Strings(sorted)
	for _, svc := range sorted {
		img := svcImage[svc]
		if !auto[svc] {
			fmt.Printf("skip   %s (not in autostart)\n", svc)
			continue
		}
		fmt.Printf("pull   %s -> %s ... ", svc, img)
		full := img
		first, _, hasSlash := strings.Cut(img, "/")
		if !hasSlash {
			full = "docker.io/library/" + img
		}
		if hasSlash && !strings.Contains(first, ".") && !strings.Contains(first, ":") && first != "localhost" {
			full = "docker.io/" + img
		}
		if sh.C("skopeo", "copy", "-q", "docker://"+full, "containers-storage:[overlay@"+store+"+"+autoRunroot+"]"+full).Quiet().OK() {
			fmt.Println("OK")
		} else {
			fmt.Println("FAIL")
		}
		// right away, users' podman can't read the store until it's fixed and their socket gives up
		podman.FixSharedPerms()
	}
	// autostart entries that have no image in compose (typos / imageless services)
	for _, svc := range autoKeys {
		if svcImage[svc] == "" {
			fmt.Printf("note   '%s' in autostart but no image found in compose\n", svc)
		}
	}
	fmt.Println()
	fmt.Printf("Fixing permissions on %s ...\n", store)
	podman.FixSharedPerms()
	fmt.Println("Done.")
	fmt.Println()
	fmt.Println("Store contents:")
	sh.C("podman", "--root", store, "images").Run()
	fmt.Println()
	sizeH, _, _ := strings.Cut(sh.Str("du", "-sh", store), "\t")
	sizeM, _, _ := strings.Cut(sh.Str("du", "-sm", store), "\t")
	mb := 0
	fmt.Sscan(sizeM, &mb)
	fmt.Printf("Shared store disk usage: %s (%.2f GB) at %s\n", sizeH, float64(mb)/1024, store)
	return 0
}

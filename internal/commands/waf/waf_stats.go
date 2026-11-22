// ##############################################################################
// Script Name: internal/commands/waf/waf_stats.go
// Description: Manage CorazaWAF
// Usage: opencli waf <status|enable|disable|domain|user|plugins|tags|ids|update|stats|count> [options]
// Author: Stefan Pejcic
// Created: 22.05.2025
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

package waf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"opencli/internal/conf"
)

// uniqC is `sort | uniq -c`
func uniqC(items []string) []string {
	sort.Strings(items)
	var out []string
	for i := 0; i < len(items); {
		j := i
		for j < len(items) && items[j] == items[i] {
			j++
		}
		out = append(out, fmt.Sprintf("%7d %s", j-i, items[i]))
		i = j
	}
	return out
}

func leadNum(s string) int {
	s = strings.TrimLeft(s, " \t")
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// sortNR is `sort -nr`, ties fall back to the whole line, reversed too
func sortNR(lines []string) []string {
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := leadNum(lines[i]), leadNum(lines[j])
		if a != b {
			return a > b
		}
		return lines[i] > lines[j]
	})
	return lines
}

func head(lines []string) []string {
	if len(lines) > 10 {
		return lines[:10]
	}
	return lines
}

// grepO is grep -o with the part after \K in group 1, empty matches skipped like grep does
func grepO(lines []string, re *regexp.Regexp) []string {
	var out []string
	for _, l := range lines {
		for _, m := range re.FindAllStringSubmatch(l, -1) {
			if m[len(m)-1] != "" {
				out = append(out, m[len(m)-1])
			}
		}
	}
	return out
}

func containsFold(lines []string, needle string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), needle) {
			out = append(out, l)
		}
	}
	return out
}

var (
	hourlyRe  = regexp.MustCompile(`\[([0-9]{4}/[0-9]{2}/[0-9]{2} [0-9]{2})`)
	ipRe      = regexp.MustCompile(`\[.*\] \S+ ([\d.]+)`)
	requestRe = regexp.MustCompile(`^((?:GET|POST|HEAD|PUT|DELETE|OPTIONS) .+)`)
	pathRe    = regexp.MustCompile(`GET\s+(\S+)`)
)

func printLines(lines []string) {
	for _, l := range lines {
		fmt.Println(l)
	}
}

func stats(mode string) {
	if !conf.IsFile(auditLog) {
		return
	}
	lines := conf.Lines(auditLog)
	switch mode {
	case "country":
		var vals []string
		for _, l := range containsFold(lines, "cf-ipcountry:") {
			vals = append(vals, strings.ReplaceAll(strings.Split(l, ":")[1], " ", ""))
		}
		printLines(sortNR(uniqC(vals)))
	case "agent":
		var vals []string
		for _, l := range containsFold(lines, "user-agent:") {
			_, rest, _ := strings.Cut(l, ":")
			vals = append(vals, rest)
		}
		printLines(head(sortNR(uniqC(vals))))
	case "hourly":
		printLines(uniqC(grepO(lines, hourlyRe)))
	case "ip":
		printLines(head(sortNR(uniqC(grepO(lines, ipRe)))))
	case "request":
		printLines(head(sortNR(uniqC(grepO(lines, requestRe)))))
	case "path":
		printLines(head(sortNR(uniqC(grepO(lines, pathRe)))))
	default:
		usage()
	}
}

// grep -oP over rules/*.conf, the file name prefix only shows with more than one file like grep does
func grepRules(re *regexp.Regexp) []string {
	files, _ := filepath.Glob(crsDir + "/rules/*.conf")
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "grep: %s/rules/*.conf: No such file or directory\n", crsDir)
		return nil
	}
	var out []string
	for _, f := range files {
		for _, m := range grepO(conf.Lines(f), re) {
			if len(files) > 1 {
				m = f + ":" + m
			}
			out = append(out, m)
		}
	}
	return out
}

func sortU(items []string) []string {
	sort.Strings(items)
	var out []string
	for i, s := range items {
		if i == 0 || s != items[i-1] {
			out = append(out, s)
		}
	}
	return out
}

var (
	tagRe = regexp.MustCompile(`tag:\s*['"]([^'"]+)`)
	idRe  = regexp.MustCompile(`id:([0-9]+)`)
)

func listTags() {
	var keep []string
	for _, l := range grepRules(tagRe) {
		if !strings.Contains(l, "OWASP_CRS") {
			keep = append(keep, l)
		}
	}
	printLines(sortU(keep))
}

func listIDs() {
	printLines(sortU(grepRules(idRe)))
}

var recordRe = regexp.MustCompile(`^--.*-B--$`)

func count() {
	if !conf.IsFile(auditLog) {
		return
	}
	n := 0
	for _, l := range conf.Lines(auditLog) {
		if recordRe.MatchString(l) {
			n++
		}
	}
	fmt.Println(n)
}

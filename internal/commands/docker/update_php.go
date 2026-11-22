// ##############################################################################
// Script Name: internal/commands/docker/update_php.go
// Description: Compose file swap of php-fpm services for docker-update.
// Usage: (internal helper)
// Author: Stefan Pejcic
// Created: 20.08.2026
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

	"opencli/internal/conf"
)

type phpSwap struct{ name, old, new string }

type phpBlock struct{ start, end int }

type phpBlocks struct {
	names []string
	pos   map[string]phpBlock
}

var (
	phpServicesRe = regexp.MustCompile(`^services:\s*$`)
	phpImageRe    = regexp.MustCompile(`^\s*image:\s*["']?([^"'\s]+)`)
	phpBindRe     = regexp.MustCompile(`^\s*- (/[^:]+):`)
)

func phpLead(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

func phpBlank(l string) bool { return strings.TrimSpace(l) == "" }

// indent of the first key under services:, files rewritten by a yaml dumper use 4
func phpUnit(lines []string) int {
	for i, l := range lines {
		if phpServicesRe.MatchString(l) {
			for _, n := range lines[i+1:] {
				if !phpBlank(n) && !strings.HasPrefix(strings.TrimLeft(n, " \t\n\v\f\r"), "#") {
					return phpLead(n)
				}
			}
		}
	}
	return 2
}

func phpFindBlocks(lines []string, u int) phpBlocks {
	out := phpBlocks{pos: map[string]phpBlock{}}
	re := regexp.MustCompile(`^` + strings.Repeat(" ", u) + `(php-fpm-[0-9.]+):\s*$`)
	i := 0
	for i < len(lines) {
		m := re.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && !(!phpBlank(lines[j]) && phpLead(lines[j]) <= u) {
			j++
		}
		k := j
		// blank lines after a block stay where they are
		for k > i+1 && phpBlank(lines[k-1]) {
			k--
		}
		if _, ok := out.pos[m[1]]; !ok {
			out.names = append(out.names, m[1])
		}
		out.pos[m[1]] = phpBlock{i, k}
		i = j
	}
	return out
}

type phpKey struct {
	name  string
	lines []string
}

// top level keys of a service block -> their lines
func phpKeys(lines []string, b phpBlock, u int) []phpKey {
	var out []phpKey
	cur := -1
	for _, l := range lines[b.start+1 : b.end] {
		if !phpBlank(l) && phpLead(l) == 2*u && !strings.HasPrefix(strings.TrimLeft(l, " \t\n\v\f\r"), "#") {
			name := strings.SplitN(strings.TrimSpace(l), ":", 2)[0]
			found := -1
			for i, k := range out {
				if k.name == name {
					found = i
				}
			}
			if found >= 0 {
				out[found].lines = nil
				cur = found
			} else {
				out = append(out, phpKey{name: name})
				cur = len(out) - 1
			}
		}
		if cur >= 0 {
			out[cur].lines = append(out[cur].lines, l)
		}
	}
	return out
}

func phpImage(lines []string, b phpBlock, u int) string {
	for _, k := range phpKeys(lines, b, u) {
		if k.name == "image" {
			if m := phpImageRe.FindStringSubmatch(k.lines[0]); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// python's text mode read turns \r\n and \r into \n
func phpRead(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n"), nil
}

// phpSwapPlan lists the php-fpm services that can move to openpanel/php-fpm, services with a missing bind source are reported on stderr text
func phpSwapPlan(tpl, path string) (todo []phpSwap, warnings []string, src, dst []string, sb, db phpBlocks, err error) {
	if src, err = phpRead(tpl); err != nil {
		return
	}
	if dst, err = phpRead(path); err != nil {
		return
	}
	su, du := phpUnit(src), phpUnit(dst)
	sb, db = phpFindBlocks(src, su), phpFindBlocks(dst, du)
	for _, name := range db.names {
		old := phpImage(dst, db.pos[name], du)
		if !strings.Contains(old, "shinsenter/php") {
			continue
		}
		sp, ok := sb.pos[name]
		if !ok {
			continue
		}
		nw := phpImage(src, sp, su)
		if !strings.HasPrefix(nw, "openpanel/php-fpm:") {
			continue
		}
		// a missing bind source (e.g. an ioncube loader the server doesn't have yet) would stop the container from starting
		var missing []string
		for _, l := range src[sp.start:sp.end] {
			if m := phpBindRe.FindStringSubmatch(l); m != nil && !conf.Exists(m[1]) {
				missing = append(missing, m[1])
			}
		}
		if len(missing) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s %s - missing %s", name, old, strings.Join(missing, " ")))
			continue
		}
		todo = append(todo, phpSwap{name, old, nw})
	}
	return
}

// phpSwapApply swaps those service blocks for the template's
func phpSwapApply(tpl, path string, wanted []string) ([]string, error) {
	todo, warnings, src, dst, sb, db, err := phpSwapPlan(tpl, path)
	if err != nil {
		return nil, err
	}
	su, du := phpUnit(src), phpUnit(dst)
	var pick []phpSwap
	for _, t := range todo {
		for _, w := range wanted {
			if w == t.name {
				pick = append(pick, t)
				break
			}
		}
	}
	// last block first so the line numbers of the others stay valid
	sort.SliceStable(pick, func(i, j int) bool { return db.pos[pick[i].name].start > db.pos[pick[j].name].start })
	for _, t := range pick {
		d := db.pos[t.name]
		s := sb.pos[t.name]
		var block []string
		for _, l := range src[s.start:s.end] {
			if phpBlank(l) {
				block = append(block, l)
			} else {
				block = append(block, strings.Repeat(" ", phpLead(l)*du/su)+strings.TrimLeft(l, " "))
			}
		}
		// keys the admin added (extra_hosts, labels...) are kept, command is shinsenter's and not needed anymore
		have := map[string]bool{}
		for _, k := range phpKeys(src, s, su) {
			have[k.name] = true
		}
		for _, k := range phpKeys(dst, d, du) {
			if !have[k.name] && k.name != "command" {
				block = append(block, k.lines...)
			}
		}
		next := append([]string{}, dst[:d.start]...)
		next = append(next, block...)
		dst = append(next, dst[d.end:]...)
	}
	return warnings, conf.WriteInPlace(path, strings.Join(dst, "\n"))
}

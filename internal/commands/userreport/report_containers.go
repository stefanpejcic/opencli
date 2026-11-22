// ##############################################################################
// Script Name: internal/commands/userreport/report_containers.go
// Description: Prints a health report for a user: what is working and what needs attention.
// Usage: opencli user-report <USERNAME|--all> [--section <name,...>] [--json]
// Author: Stefan Pejcic
// Created: 02.10.2026
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

package userreport

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// bashHash is bash's FNV-1 string hash for associative arrays
func bashHash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h *= 16777619
		h ^= uint32(int32(int8(s[i])))
	}
	return h
}

// assocOrder is the order "${!arr[@]}" lists keys inserted in this order
func assocOrder(keys []string) []string {
	out := append([]string{}, keys...)
	pos := map[string]int{}
	for i, k := range keys {
		pos[k] = i
	}
	sort.SliceStable(out, func(a, b int) bool {
		ha, hb := bashHash(out[a])&1023, bashHash(out[b])&1023
		if ha != hb {
			return ha < hb
		}
		return pos[out[a]] > pos[out[b]]
	})
	return out
}

// jqNum formats a computed number like jq prints it
func jqNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e17 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// jqPath walks .a.b.c, nil when anything is missing
func jqPath(v any, keys ...string) any {
	for _, k := range keys {
		o, ok := v.(*ojson.Object)
		if !ok {
			return nil
		}
		v, _ = o.Get(k)
	}
	return v
}

func numOf(v any) float64 {
	switch t := v.(type) {
	case json.Number:
		f, _ := t.Float64()
		return f
	case float64:
		return t
	case bool:
		if t {
			return 1
		}
	}
	return 0
}

// cG is awk's printf "%g"
func cG(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }

// pf0 is bash printf '%.0f'
func pf0(s string) string { return fmt.Sprintf("%.0f", awkNum(s)) }

// startedEpoch parses podman's StartedAt the way the script fed it to date -d
func startedEpoch(started string, now int64) int64 {
	s := started
	if loc := regexpFirstFrac.FindStringIndex(s); loc != nil {
		s = s[:loc[0]] + s[loc[1]:]
	}
	s = regexpTZName.ReplaceAllString(s, "")
	if t, err := time.Parse("2006-01-02 15:04:05 -0700", s); err == nil {
		return t.Unix()
	}
	if out, err := sh.C("date", "-d", s, "+%s").NoErr().Out(); err == nil {
		return bashInt(out)
	}
	return now
}

func secContainers(s *sec) {
	u := s.u
	s.section("CONTAINERS")
	u.needPS()
	psOut := u.ps
	if !u.psOK {
		s.err("Can't reach the user's podman, none of the account's services can be checked or managed.")
		containersUsage(s)
		return
	}
	if psOut == "" {
		s.warn("No containers exist for this account.")
		containersUsage(s)
		return
	}

	// live usage of running containers, keyed by name
	sCPU, sMem, sMemp, sPids := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	if v, err := ojson.Parse(u.pod(20*time.Second, "stats", "--no-stream", "--format", "json").Str()); err == nil {
		if arr, ok := v.([]any); ok {
			for _, it := range arr {
				var f []string
				for _, k := range []string{"name", "cpu_percent", "mem_usage", "mem_percent", "pids"} {
					f = append(f, tsvField(jqPath(it, k)))
				}
				r := bread(strings.Join(f, "\t"), "\t", 5)
				if r[0] == "" {
					continue
				}
				sCPU[r[0]] = strings.TrimSuffix(r[1], "%")
				sMem[r[0]] = r[2]
				sMemp[r[0]] = strings.TrimSuffix(r[3], "%")
				sPids[r[0]] = r[4]
			}
		}
	}

	// limits and history from inspect
	type insp struct{ cpus, mem, pids, restarts, oom, exit, started, hlog, htest string }
	in := map[string]insp{}
	var ids []string
	for _, l := range strings.Split(psOut, "\n") {
		ids = append(ids, strings.Fields(idxS(strings.Split(l, "\t"), 3))...)
	}
	args := append([]string{"inspect"}, ids...)
	args = append(args, "--format", `{{.Name}}	{{.HostConfig.NanoCpus}}	{{.HostConfig.Memory}}	{{.HostConfig.PidsLimit}}	{{.RestartCount}}	{{.State.OOMKilled}}	{{.State.ExitCode}}	{{.State.StartedAt}}	{{len .State.Health.Log}}	{{if .Config.Healthcheck}}{{len .Config.Healthcheck.Test}}{{else}}0{{end}}`)
	insOut, _ := u.pod(20*time.Second, args...).Raw()
	for _, l := range readLines(insOut) {
		f := bread(l, "\t", 10)
		if f[0] == "" {
			continue
		}
		in[f[0]] = insp{f[1], f[2], f[3], f[4], f[5], f[6], f[7], f[8], f[9]}
	}

	var noChecks []string

	// where each container comes from: panel template, a panel app (sites table), the user's own, or not in compose at all
	src := map[string]string{}
	var srcKeys []string
	for _, svc := range hereLines(u.needServices()) {
		if svc != "" {
			if _, ok := src[svc]; !ok {
				srcKeys = append(srcKeys, svc)
			}
			src[svc] = "custom"
		}
	}
	for _, svc := range hereLines(s.r.defaultServices()) {
		if src[svc] != "" {
			src[svc] = "default"
		}
	}
	for _, l := range strings.Split(u.siteRows, "\n") {
		f := strings.Split(l, "\t")
		if idxS(f, 3) == "" {
			continue
		}
		r := bread(idxS(f, 3)+"\t"+f[0], "\t", 2)
		svc := strings.ToLower(r[0])
		if src[svc] == "custom" {
			src[svc] = "app:" + r[1]
		}
	}
	var custom []string
	now := time.Now().Unix()
	total, running := 0, 0
	c := s.r
	s.p("  %s%s %s %s %s %s %s %s%s\n", c.cDim, padR("Container", 18), padR("Source", 8), padR("Status", 30), padR("CPU", 16), padR("RAM", 26), padR("PIDs", 11), "Restarts", c.cReset)
	for _, l := range hereLines(psOut) {
		f := bread(l, "\t", 4)
		name, state, status := f[0], f[1], f[2]
		total++
		i := in[name]
		cores := cG(awkNum(or(i.cpus, "0")) / 1e9)
		cpuTxt, memTxt, pidsTxt, cpuPct := "-", "-", "-", ""
		if state == "running" {
			running++
			if sCPU[name] != "" {
				// percent of the container's own cpu limit, same math as the containers page
				if cores != "0" {
					cpuPct = fmt.Sprintf("%.0f", awkNum(sCPU[name])/awkNum(cores))
					cpuTxt = cpuPct + "% of " + cores
				} else {
					cpuTxt = sCPU[name] + "%"
				}
				memTxt = strings.ReplaceAll(sMem[name], " ", "") + " (" + pf0(or(sMemp[name], "0")) + "%)"
				pidsTxt = sPids[name] + "/" + or(i.pids, "?")
			}
		}
		// podman-compose leaves an empty health check on containers without one and podman shows that as "(starting)" forever
		if or(i.htest, "0") == "0" {
			status = strings.TrimSuffix(status, " (starting)")
		}
		srcV := or(src[name], "manual")
		var srcTxt string
		switch {
		case srcV == "default":
			srcTxt = "default"
		case strings.HasPrefix(srcV, "app:"):
			srcTxt = c.cCyan + "app" + c.cReset + "    "
			custom = append(custom, name+" (app for "+srcV[4:]+")")
		case srcV == "custom":
			srcTxt = c.cYellow + "custom" + c.cReset + " "
			custom = append(custom, name+" (added to docker-compose.yml)")
		default:
			srcTxt = c.cYellow + "manual" + c.cReset + " "
			custom = append(custom, name+" (not in docker-compose.yml)")
		}
		s.p("  %s %s %s %s %s %s %s\n", padR(name, 18), padR(srcTxt, 8), padR(runeCut(status, 30), 30), padR(cpuTxt, 16), padR(memTxt, 26), padR(pidsTxt, 11), or(i.restarts, "0"))

		// problems
		upSecs := now - startedEpoch(i.started, now)
		if i.oom == "true" {
			s.warn(name + " was killed for running out of memory (limit " + humanBytes(i.mem) + ").")
		}
		if state != "running" {
			if or(i.exit, "0") != "0" {
				s.warn(name + " is stopped, it exited with code " + i.exit + ".")
			}
			continue
		}
		if strings.Contains(status, "(unhealthy)") {
			s.warn(name + " is unhealthy, its health check is failing.")
		} else if strings.Contains(status, "(starting)") && upSecs > 600 {
			// no health log at all means podman never ran the check, not that it failed
			if or(i.hlog, "0") == "0" {
				noChecks = append(noChecks, name)
			} else {
				s.warn(name + " health check hasn't passed after " + humanAge(upSecs) + ".")
			}
		}
		if cpuPct != "" && bashInt(cpuPct) >= 90 {
			s.warn(name + " is using " + cpuPct + "% of its CPU limit (" + cores + " cores).")
		}
		if sMemp[name] != "" && awkNum(sMemp[name]) >= 90 {
			s.warn(name + " is using " + pf0(sMemp[name]) + "% of its memory limit.")
		}
		if reDigits.MatchString(sPids[name]) && reDigits.MatchString(i.pids) {
			sp, ip := bashInt(sPids[name]), bashInt(i.pids)
			if ip > 0 && sp*100/ip >= 90 {
				s.warn(name + " is using " + sPids[name] + " of " + i.pids + " processes.")
			}
		}
		if reDigits.MatchString(i.restarts) && bashInt(i.restarts) >= 5 {
			s.warn(name + " restarted " + i.restarts + " times, it may be crashing.")
		}
	}
	s.line("")
	s.row("Containers", fmt.Sprintf("%d running, %d stopped", running, total-running))
	// custom services in the compose file that were never created
	names := map[string]bool{}
	for _, l := range hereLines(psOut) {
		names[strings.Split(l, "\t")[0]] = true
	}
	for _, svc := range assocOrder(srcKeys) {
		if src[svc] == "default" || names[svc] {
			continue
		}
		custom = append(custom, svc+" (in docker-compose.yml, not created)")
	}
	label := "Not from the template"
	for _, x := range custom {
		s.row(label, x)
		label = ""
	}
	if len(noChecks) > 0 {
		s.info("Health checks never ran for " + strings.Join(noChecks, ", ") + ", so their health is unknown.")
	}

	if v, err := ojson.Parse(u.pod(20*time.Second, "images", "--format", "json").Str()); err == nil {
		if arr, ok := v.([]any); ok {
			sum := 0.0
			for _, it := range arr {
				sum += numOf(jqPath(it, "Size"))
			}
			s.row("Images", fmt.Sprintf("%d, %s", len(arr), humanBytes(jqNum(sum))))
		}
	}

	containersUsage(s)
}

// lastLine is tail -n 1
func lastLine(content string) string {
	l := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	return l[len(l)-1]
}

// cpuPctOf is (.cpu.usage.pct * 100 / .cpu.total.pct) when total is set, else 0
func cpuPctOf(o any) float64 {
	t := numOf(jqPath(o, "cpu", "total", "pct"))
	if t > 0 {
		return numOf(jqPath(o, "cpu", "usage", "pct")) * 100 / t
	}
	return 0
}

// containersUsage is account-wide usage from resource_usage.txt, written hourly by 'opencli docker-collect_stats'
func containersUsage(s *sec) {
	s.subsection("ACCOUNT USAGE")
	file := "/home/" + s.u.context + "/resource_usage.txt"
	content := readFile(file)
	last := lastLine(content)
	o, ok := jqValid(last)
	if !ok {
		s.warn("No usage data collected yet (" + file + ").")
		return
	}
	var f []string
	for _, p := range [][]string{{"timestamp"}, {"memory", "used", "human"}, {"memory", "total", "human"}, {"memory", "usage_pct"}, {"cpu", "usage", "human"}, {"cpu", "total", "human"}} {
		f = append(f, tsvField(jqPath(o, p...)))
	}
	f = append(f, jqNum(math.Floor(cpuPctOf(o))))
	for _, p := range [][]string{{"tasks", "current"}, {"tasks", "limit"}, {"tasks", "usage_pct"}} {
		f = append(f, tsvField(jqPath(o, p...)))
	}
	r := bread(strings.Join(f, "\t"), "\t", 10)
	ts, memUsed, memTotal, memPct, cpuUsed, cpuTotal, cpuPct, tasks, tasksLimit, tasksPct := r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8], r[9]
	now := time.Now().Unix()
	tsE := now
	if out, err := sh.C("date", "-d", ts, "+%s").NoErr().Out(); err == nil {
		tsE = bashInt(out)
	}
	age := now - tsE
	s.row("Last sample", ts+" ("+humanAge(age)+" ago)")
	if age > 7200 {
		s.warn("Usage stats haven't been collected for " + humanAge(age) + ", check the 'opencli docker-collect_stats' cron.")
	}
	s.row("RAM", memUsed+" of "+memTotal+" ("+memPct+"%)")
	s.row("CPU", cpuUsed+" of "+cpuTotal+" ("+cpuPct+"%)")
	s.row("Processes", tasks+" of "+tasksLimit+" ("+tasksPct+"%)")

	// same thresholds as the dashboard
	mp, cp, tp := bashInt(memPct), bashInt(cpuPct), bashInt(tasksPct)
	if mp >= 100 {
		s.err("Account RAM is at " + memPct + "% of the plan limit.")
	} else if mp >= 90 {
		s.warn("Account RAM is at " + memPct + "% of the plan limit.")
	}
	if cp >= 100 {
		s.err("Account CPU is at " + cpuPct + "% of the plan limit.")
	} else if cp >= 90 {
		s.warn("Account CPU is at " + cpuPct + "% of the plan limit.")
	}
	if tp >= 90 {
		s.warn("Account is using " + tasksPct + "% of its process limit.")
	}

	// last 24 samples (hourly), averages and peaks
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if len(lines) > 24 {
		lines = lines[len(lines)-24:]
	}
	var samples []any
	okAll := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		v, err := ojson.Parse(l)
		if err != nil {
			okAll = false
			break
		}
		if _, isObj := v.(*ojson.Object); isObj {
			samples = append(samples, v)
		}
	}
	if !okAll || len(samples) == 0 {
		return
	}
	n := float64(len(samples))
	memSum, memMax, cpuSum, cpuMax, hot := 0.0, math.Inf(-1), 0.0, math.Inf(-1), 0
	var memMaxRaw any
	for _, x := range samples {
		m := numOf(jqPath(x, "memory", "usage_pct"))
		memSum += m
		if m > memMax || memMaxRaw == nil {
			if m > memMax {
				memMax = m
				memMaxRaw = jqPath(x, "memory", "usage_pct")
			}
		}
		cpv := cpuPctOf(x)
		cpuSum += cpv
		if cpv > cpuMax {
			cpuMax = cpv
		}
		if m >= 90 || (numOf(jqPath(x, "cpu", "total", "pct")) > 0 && cpv >= 90) {
			hot++
		}
	}
	memMaxS := jqNum(memMax)
	if nmb, ok := memMaxRaw.(json.Number); ok {
		memMaxS = nmb.String()
	}
	s.row(fmt.Sprintf("Last %d samples", len(samples)), fmt.Sprintf("RAM avg %s%%, peak %s%% / CPU avg %s%%, peak %s%%",
		jqNum(math.Floor(memSum/n)), memMaxS, jqNum(math.Floor(cpuSum/n)), jqNum(math.Floor(cpuMax))))
	if hot > 0 {
		s.info(fmt.Sprintf("Account was at 90%% or more of its CPU or RAM limit in %d of the last %d hourly samples.", hot, len(samples)))
	}
}

// sortRN is sort -rn on a field (0 = whole line), ties fall back to the whole line reversed
func sortRN(lines []string, key func(string) string) []string {
	out := append([]string{}, lines...)
	sort.SliceStable(out, func(a, b int) bool {
		na, nb := sortNum(key(out[a])), sortNum(key(out[b]))
		if na != nb {
			return na > nb
		}
		return out[a] > out[b]
	})
	return out
}

// sortNum is the number sort -n sees at the start of a key
func sortNum(s string) float64 {
	s = strings.TrimLeft(s, " \t")
	end := 0
	if end < len(s) && s[end] == '-' {
		end++
	}
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.') {
		end++
	}
	f, _ := strconv.ParseFloat(s[:end], 64)
	return f
}

func tabField(l string, n int) string { return idxS(strings.Split(l, "\t"), n-1) }

func secProcesses(s *sec) {
	u := s.u
	s.section("PROCESSES")
	u.needPS()
	running := u.running
	if !u.psOK {
		s.err("Can't reach the user's podman, processes can't be listed.")
		return
	}
	if running == "" {
		s.row("Processes", "none, no containers are running")
		return
	}

	// host pid -> container and in-container user, from podman top
	pCtr, pUser := map[string]string{}, map[string]string{}
	var pids []string
	for _, c := range hereLines(running) {
		for _, l := range readLines(u.pod(20*time.Second, "top", c, "hpid", "user").Str() + "\n") {
			f := bread(l, " \t\n", 3)
			if !reDigits.MatchString(f[0]) {
				continue
			}
			if _, ok := pCtr[f[0]]; !ok {
				pids = append(pids, f[0])
			}
			pCtr[f[0]], pUser[f[0]] = c, f[1]
		}
	}
	if len(pCtr) == 0 {
		s.warn("podman top returned no processes for the running containers.")
		return
	}

	// real RAM and state come from the host ps, podman top has no rss
	psOut := sh.C("ps", "-o", "pid=,stat=,rss=,pcpu=,etimes=,args=", "-p", strings.Join(assocOrder(pids), ",")).NoErr().Str()
	var rowsL []string
	for _, l := range strings.Split(psOut, "\n") {
		if l == "" {
			continue
		}
		f := strings.Fields(l)
		for len(f) < 5 {
			f = append(f, "")
		}
		rowsL = append(rowsL, strings.Join(f[:5], "\t")+"\t"+strings.Join(f[5:], " "))
	}
	rows := strings.Join(rowsL, "\n")

	perCtr, perRSS := map[string]int64{}, map[string]int64{}
	var ctrKeys, zombies []string
	total, totalRSS := 0, int64(0)
	for _, l := range hereLines(rows) {
		f := bread(l, "\t", 6)
		if f[0] == "" {
			continue
		}
		c := pCtr[f[0]]
		total++
		if _, ok := perCtr[c]; !ok {
			ctrKeys = append(ctrKeys, c)
		}
		perCtr[c]++
		perRSS[c] += bashInt(f[2])
		totalRSS += bashInt(f[2])
		if strings.HasPrefix(f[1], "Z") {
			zombies = append(zombies, c)
		}
	}

	s.row("Processes", fmt.Sprintf("%d in %d containers, %s RAM", total, len(hereLines(running)), humanKB(fmt.Sprint(totalRSS))))
	s.p("  %s%s %s %s%s\n", s.r.cDim, padR("Container", 24), padR("Processes", 10), "RAM", s.r.cReset)
	var kl []string
	for _, k := range assocOrder(ctrKeys) {
		kl = append(kl, fmt.Sprintf("%d %s", perRSS[k], k))
	}
	for _, l := range sortRN(kl, func(x string) string { return x }) {
		c := strings.Fields(l)
		k := idxS(c, 1)
		s.p("  %s %s %s\n", padR(k, 24), padR(fmt.Sprint(perCtr[k]), 10), humanKB(fmt.Sprint(perRSS[k])))
	}

	// ps %cpu is the average over the process lifetime, good enough to spot hogs
	header := fmt.Sprintf("  %s%s %s %s %s %s %s %s%s", s.r.cDim, padR("Container", 16), padR("Host PID", 9), padR("User", 10), padR("CPU%", 7), padR("RAM", 10), padR("Running", 9), "Command", s.r.cReset)
	procRows := func(lines []string) {
		if len(lines) > 5 {
			lines = lines[:5]
		}
		for _, l := range lines {
			f := bread(l, "\t", 6)
			s.p("  %s %s %s %s %s %s %s\n", padR(runeCut(pCtr[f[0]], 16), 16), padR(f[0], 9), padR(runeCut(pUser[f[0]], 10), 10), padR(f[3], 7), padR(humanKB(f[2]), 10), padR(humanAge(bashInt(f[4])), 9), runeCut(f[5], 50))
		}
	}
	s.subsection("TOP BY CPU")
	s.line(header)
	procRows(sortRN(hereLines(rows), func(x string) string { return tabField(x, 4) }))
	s.subsection("TOP BY RAM")
	s.line(header)
	procRows(sortRN(hereLines(rows), func(x string) string { return tabField(x, 3) }))

	// problems
	if len(zombies) > 0 {
		uz := append([]string{}, zombies...)
		sort.Strings(uz)
		uz = uniq(uz)
		s.warn(fmt.Sprintf("%d zombie process(es) in %s, their parent isn't reaping them.", len(zombies), strings.Join(uz, ", ")))
	}
	for _, l := range hereLines(rows) {
		f := bread(l, "\t", 6)
		if f[0] == "" {
			continue
		}
		if awkNum(f[3]) >= 90 && bashInt(f[4]) >= 600 {
			s.warn("Process " + f[0] + " in " + pCtr[f[0]] + " (" + runeCut(f[5], 60) + ") has used " + f[3] + "% CPU for " + humanAge(bashInt(f[4])) + ".")
		}
	}
}

// uniq drops adjacent duplicates of a sorted list
func uniq(a []string) []string {
	var out []string
	for i, x := range a {
		if i == 0 || x != a[i-1] {
			out = append(out, x)
		}
	}
	return out
}

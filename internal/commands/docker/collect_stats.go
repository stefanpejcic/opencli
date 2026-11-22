// ##############################################################################
// Script Name: internal/commands/docker/collect_stats.go
// Description: Collect resource usage information for user(s).
// Usage: opencli docker-collect_stats <username|--all>
// Author: Stefan Pejcic
// Created: 22.07.2025
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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "docker-collect_stats",
		Desc:  "Collect resource usage information for user(s).",
		Usage: "opencli docker-collect_stats <username|--all>",
		Run:   collectStats,
	})
}

var statsRetentionRe = regexp.MustCompile(`resource_usage_retention=[0-9]+`)

type statsServer struct {
	memory    int64
	cpus      int
	retention int
}

func statsHuman(n float64) string {
	switch {
	case n > 1073741824:
		return fmt.Sprintf("%.1fG", n/1073741824)
	case n > 1048576:
		return fmt.Sprintf("%.1fM", n/1048576)
	case n > 1024:
		return fmt.Sprintf("%.1fK", n/1024)
	}
	return awkNum(n) + "B"
}

func statsHumanBits(n float64) string {
	switch {
	case n > 1000000000:
		return fmt.Sprintf("%.1fGbit", n/1000000000)
	case n > 1000000:
		return fmt.Sprintf("%.1fMbit", n/1000000)
	case n > 1000:
		return fmt.Sprintf("%.1fKbit", n/1000)
	}
	return awkNum(n) + "bit"
}

// awk prints integral numbers without decimals
func awkNum(n float64) string {
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', 6, 64)
}

func statsCores(p float64) string { return fmt.Sprintf("%.1f cores", p/100) }

func statsNum(s string) float64 {
	s = strings.TrimSpace(s)
	m := regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?`).FindString(s)
	f, _ := strconv.ParseFloat(m, 64)
	return f
}

func statsFirstLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func statsUsec(path string) string {
	for _, l := range strings.Split(conf.Read(path), "\n") {
		if strings.HasPrefix(l, "usage_usec") {
			f := strings.Fields(l)
			if len(f) > 1 {
				return f[1]
			}
			return ""
		}
	}
	return ""
}

func (s *statsServer) processUser(user string, delay float64) {
	st, err := os.Stat("/home/" + user)
	if err != nil {
		fmt.Fprintln(os.Stderr, `{"error": "Context `+user+` not found"}`)
		return
	}
	uid := strconv.Itoa(int(st.Sys().(*syscall.Stat_t).Uid))
	cgroup := "/sys/fs/cgroup/user.slice/user-" + uid + ".slice"
	if !conf.IsDir(cgroup) {
		fmt.Fprintln(os.Stderr, `{"error": "Cgroup for `+user+` not found (`+cgroup+`)"}`)
		return
	}

	cpu1 := statsUsec(cgroup + "/cpu.stat")
	t1 := time.Now().UnixNano()

	memCurrent := strings.TrimRight(conf.Read(cgroup+"/memory.current"), "\n")
	memMax := strings.TrimRight(conf.Read(cgroup+"/memory.max"), "\n")
	if n, err := strconv.ParseInt(strings.TrimSpace(memMax), 10, 64); memMax == "" || memMax == "max" || (err == nil && n == 0) {
		memMax = strconv.FormatInt(s.memory, 10)
	}
	memStat := conf.Read(cgroup + "/memory.stat")

	pidsCurrent := strings.TrimRight(conf.Read(cgroup+"/pids.current"), "\n")
	pidsMax := strings.TrimRight(conf.Read(cgroup+"/pids.max"), "\n")

	time.Sleep(time.Duration(delay * float64(time.Second)))

	cpu2 := statsUsec(cgroup + "/cpu.stat")
	t2 := time.Now().UnixNano()

	cpuTotalServer := s.cpus * 100
	cpuMaxPct := cpuTotalServer
	if conf.Exists(cgroup + "/cpu.max") {
		f := strings.Fields(statsFirstLine(conf.Read(cgroup + "/cpu.max")))
		quota, period := "", ""
		if len(f) > 0 {
			quota = f[0]
		}
		if len(f) > 1 {
			period = f[1]
		}
		digits := func(x string) string {
			return strings.Map(func(r rune) rune { return map[bool]rune{true: r, false: -1}[r >= '0' && r <= '9'] }, x)
		}
		q, _ := strconv.Atoi(digits(quota))
		p, _ := strconv.Atoi(digits(period))
		if q != 0 && p != 0 {
			cpuMaxPct = q * 100 / p
		}
	}
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// everything below is awk's double math
	var anon, file, kernel float64
	for _, l := range strings.Split(memStat, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "anon":
			anon = statsNum(f[1])
		case "file":
			file = statsNum(f[1])
		case "kernel":
			kernel = statsNum(f[1])
		}
	}
	mm := statsNum(memMax)
	mc := statsNum(memCurrent)
	used := anon + kernel
	buff := file
	free := mm - mc
	avail := free + buff
	memPct := int64(used / mm * 100)

	cpuDelta := statsNum(cpu2) - statsNum(cpu1)
	intervalUs := int64((float64(t2) - float64(t1)) / 1000)
	cpuUsage := int64(0)
	if intervalUs > 0 {
		cpuUsage = int64(cpuDelta / float64(intervalUs) * 100)
	}
	cpuLimit := int64(0)
	if cpuMaxPct > 0 {
		cpuLimit = int64(float64(cpuUsage) / float64(cpuMaxPct) * 100)
	}

	tasksLimit := "null"
	tasksPct := int64(0)
	if pidsMax != "max" && pidsMax != "" {
		lim := statsNum(pidsMax)
		tasksLimit = awkNum(lim)
		if lim > 0 {
			tasksPct = int64(statsNum(pidsCurrent) / lim * 100)
		}
	}

	warn := "null"
	wmsg := ""
	if memPct >= 85 {
		wmsg = fmt.Sprintf("Memory at %d%%", memPct)
	}
	if cpuLimit >= 85 {
		if wmsg != "" {
			wmsg += ", "
		}
		wmsg += fmt.Sprintf("CPU at %d%%", cpuLimit)
	}
	if tasksPct >= 85 {
		if wmsg != "" {
			wmsg += ", "
		}
		wmsg += fmt.Sprintf("Tasks at %d%%", tasksPct)
	}
	if wmsg != "" {
		warn = `"` + wmsg + " — above threshold\""
	}

	// bandwidth used to come from tc stats in dockerd's netns, no podman equivalent yet so it's 0 and kept in the json so consumers don't break
	usage := fmt.Sprintf(`{"timestamp":"%s","user":"%s","uid":%s,"memory":{"total":{"bytes":%d,"human":"%s"},"used":{"bytes":%d,"human":"%s"},"free":{"bytes":%d,"human":"%s"},"buff_cache":{"bytes":%d,"human":"%s"},"available":{"bytes":%d,"human":"%s"},"usage_pct":%d},"cpu":{"usage":{"pct":%d,"human":"%s"},"total":{"pct":%d,"human":"%s"},"server":{"pct":%d,"human":"%s"}},"tasks":{"current":%d,"limit":%s,"usage_pct":%d},"bandwidth":{"limit":{"bits":%d,"human":"%s"},"total_sent":{"bytes":%d,"human":"%s"},"usage_pct":%d},"warning":%s}`,
		timestamp, user, uid,
		int64(mm), statsHuman(mm),
		int64(used), statsHuman(used),
		int64(free), statsHuman(free),
		int64(buff), statsHuman(buff),
		int64(avail), statsHuman(avail),
		memPct,
		cpuLimit, statsCores(float64(cpuLimit)),
		cpuMaxPct, statsCores(float64(cpuMaxPct)),
		cpuTotalServer, statsCores(float64(cpuTotalServer)),
		int64(statsNum(pidsCurrent)), tasksLimit, tasksPct,
		0, statsHumanBits(0),
		0, statsHumanBits(0),
		0,
		warn)

	usageFile := "/home/" + user + "/resource_usage.txt"
	if f, err := os.OpenFile(usageFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666); err == nil {
		f.WriteString(usage + "\n")
		f.Close()
	}
	b, _ := os.ReadFile(usageFile)
	total := strings.Count(string(b), "\n")
	if s.retention > 0 && total > s.retention {
		lines := strings.SplitAfter(string(b), "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		keep := lines[len(lines)-s.retention:]
		if os.WriteFile(usageFile+".tmp", []byte(strings.Join(keep, "")), 0o666) == nil {
			os.Rename(usageFile+".tmp", usageFile)
		}
	}
	fmt.Println(usage)
}

func collectStats(args []string) int {
	retention := 100
	if m := statsRetentionRe.FindString(conf.Read(conf.Panel)); m != "" {
		retention, _ = strconv.Atoi(strings.TrimPrefix(m, "resource_usage_retention="))
	}
	db.Require()

	lock, err := os.OpenFile("/root/openpanel_docker_collect_stats.lock", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err == nil {
		defer lock.Close()
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			fmt.Println("Error: Script already running.")
			return 1
		}
	}

	if len(args) != 1 {
		fmt.Println("Usage: opencli docker-collect_stats <username|--all>")
		return 1
	}

	s := &statsServer{retention: retention}
	for _, l := range conf.Lines("/proc/meminfo") {
		if strings.Contains(l, "MemTotal") {
			if f := strings.Fields(l); len(f) > 1 {
				kb, _ := strconv.ParseInt(f[1], 10, 64)
				s.memory = kb * 1024
			}
		}
	}
	s.cpus = runtime.NumCPU()
	if out := sh.Str("nproc"); out != "" {
		s.cpus, _ = strconv.Atoi(out)
	}

	var wg sync.WaitGroup
	if args[0] == "--all" {
		if sh.Has("repquota") {
			sh.Opencli("user-quota").Quiet().Run()
		}
		users := userContexts(sh.Opencli("user-list", "--local", "--json").Timeout(20 * time.Second))
		if len(users) == 0 {
			return 0
		}
		maxJobs := s.cpus * 2
		if maxJobs > 8 {
			maxJobs = 8
		}
		if maxJobs < 2 {
			maxJobs = 2
		}
		sem := make(chan struct{}, maxJobs)
		const jitterWindow = 0.5
		for i, u := range users {
			d := float64(i) / float64(len(users)) * jitterWindow
			if d < 0.1 {
				d = 0.1
			}
			jitter, _ := strconv.ParseFloat(fmt.Sprintf("%.2f", d), 64)
			sem <- struct{}{}
			wg.Add(1)
			go func(u string) {
				defer wg.Done()
				s.processUser(u, jitter)
				<-sem
			}(u)
		}
	} else {
		s.processUser(args[0], 0.1)
	}
	wg.Wait()
	return 0
}

// ##############################################################################
// Script Name: internal/commands/plan/apply.go
// Description: Change plan for a user and apply new plan limits.
// Usage: opencli plan-apply <PLAN_ID> <USERNAME>... [--all] [--cpu] [--ram] [--dsk] [--net] [--email] [--debug]
// Author: Stefan Pejcic
// Created: 17.11.2023
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

package plan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opencli/internal/cli"
	"opencli/internal/db"
	"opencli/internal/podman"
	"opencli/internal/requirement"
	"opencli/internal/sh"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "plan-apply",
		Desc:  "Change plan for a user and apply new plan limits.",
		Usage: `opencli plan-apply <PLAN_ID> <USERNAME>... [--all] [--cpu] [--ram] [--dsk] [--net] [--email] [--debug]`,
		Run:   apply,
	})
}

func applyUsage() int {
	fmt.Println("Usage: opencli plan-apply <plan_id> <username1> <username2>... [--debug] [--all] [--cpu] [--ram] [--dsk] [--net] [--email]")
	return 1
}

func applyLimitText(value, unit, description string) string {
	if value == "0" {
		return description + " limit removed (unlimited)."
	}
	return description + " limit changed to " + value + unit + "."
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// bc prints a product with the operands' scale, so 2.5 * 100 is 250.0
func applyCPUPercent(cpu string) string {
	i, f, has := strings.Cut(cpu, ".")
	if !has {
		return strconv.Itoa(toInt(i) * 100)
	}
	d := len(f)
	p := f
	for len(p) < 2 {
		p += "0"
	}
	rest := p[2:]
	for len(rest) < d {
		rest += "0"
	}
	return strconv.Itoa(toInt(i)*100+toInt(p[:2])) + "." + rest
}

func toInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func fileUID(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stat: cannot statx '%s': No such file or directory\n", path)
		return ""
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return strconv.Itoa(int(s.Uid))
	}
	return ""
}

func apply(args []string) int {
	if len(args) < 2 {
		return applyUsage()
	}
	planID := args[0]
	var usernames []string
	partial, debug, bulk, docpu, doram, dodsk, donet, doemail := false, false, false, false, false, false, false, false
	for _, a := range args[1:] {
		switch a {
		case "--debug":
			debug = true
		case "--all":
			bulk = true
		case "--cpu":
			partial, docpu = true, true
		case "--ram":
			partial, doram = true, true
		case "--dsk":
			partial, dodsk = true, true
		case "--net":
			partial, donet = true, true
		case "--email":
			partial, doemail = true, true
		default:
			if strings.HasPrefix(a, "--") {
				return applyUsage()
			}
			usernames = append(usernames, a)
		}
	}

	db.Require()
	requirement.Command("mariadb", "mariadb-client")

	var cpu, ram, diskLimit, inodes, hourly string
	rows := db.Split(db.Cmd("SELECT cpu, ram, disk_limit, inodes_limit, max_hourly_email, bandwidth FROM plans WHERE id = '"+db.Escape(planID)+"' LIMIT 1;", "-N", "-B").Str())
	if len(rows) > 0 {
		f := append(rows[0], "", "", "", "", "", "")
		cpu, ram, diskLimit, inodes, hourly = f[0], f[1], f[2], f[3], f[4]
	}
	numDisk, _, _ := strings.Cut(strings.TrimSpace(diskLimit), " ")
	blocks := strconv.Itoa(toInt(numDisk) * 1024000)

	ramText := applyLimitText(digitsOnly(ram), "GB", "total")
	cpuText := applyLimitText(cpu, " core(s)", "total")
	diskText := applyLimitText(blocks, " blocks", "total")
	inodesText := applyLimitText(inodes, " inodes", "total")
	hourlyText := applyLimitText(hourly, "", "max hourly emails for all domains")

	if bulk {
		usernames = nil
		for _, r := range db.Rows("SELECT username FROM users WHERE plan_id = '" + db.Escape(planID) + "';") {
			usernames = append(usernames, r[0])
		}
		if debug {
			fmt.Println("Applying plan changes to users: " + strings.Join(usernames, " "))
		}
	}

	total := len(usernames)
	for n, username := range usernames {
		fmt.Println("+=============================================================================+")
		fmt.Printf("Processing user: %s (%d/%d)\n", username, n+1, total)
		fmt.Println("")

		context := ""
		for _, r := range db.Rows("SELECT plan_id, server FROM users WHERE username = '" + db.Escape(username) + "'") {
			f := strings.Fields(strings.Join(r, " "))
			if len(f) > 1 {
				context = strings.Join(f[1:], " ")
			}
			break
		}
		if context == "" || context == "NULL" {
			fmt.Printf("ERROR: No context/server found for user '%s' - skipping.\n", username)
			continue
		}
		fileUID("/home/" + context)
		uid := fileUID("/home/" + username)
		slice := "user-" + uid + ".slice"

		// cpu and ram together need the slice with delegation first
		if !partial || (docpu && doram) {
			override := "/etc/systemd/system/user-" + uid + ".slice.d/override.conf"
			if _, err := os.Stat(override); err != nil || !isFile(override) {
				os.MkdirAll("/etc/systemd/system/user-"+uid+".slice.d/", 0o755)
				os.WriteFile(override, []byte("[Slice]\nDelegate=yes\n"), 0o644)
				sh.C("systemctl", "daemon-reload").Run()
				sh.C("systemctl", "restart", "user@"+uid+".service").Run()
				// restarting user@ takes the podman socket down with it, bring it back
				for i := 0; i < 30; i++ {
					if sh.C("systemctl", "is-active", "user@"+uid+".service").Quiet().OK() {
						break
					}
					time.Sleep(time.Second)
				}
				sh.C("systemctl", "--user", "-M", username+"@", "enable", "--now", "podman.socket").Quiet().Run()
			}
		}

		if !partial || doram {
			ram = strings.TrimSuffix(ram, "G")
			ram = strings.TrimSuffix(ram, "g")
			if toInt(ram) == 0 && !strings.Contains(ram, ".") {
				_, high, max := podman.UnlimitedSliceCeiling()
				tasks := podman.TasksMax(max/1024, context)
				sh.C("systemctl", "set-property", slice, fmt.Sprintf("MemoryHigh=%dM", high), fmt.Sprintf("MemoryMax=%dM", max)).Run()
				sh.C("systemctl", "set-property", slice, fmt.Sprintf("TasksMax=%d", tasks)).Run()
				fmt.Printf("- Ceiling:    [OK]   Unlimited RAM, safety ceiling at %dMB (throttled) / %dMB (max), %d tasks.\n", high, max, tasks)
			} else {
				tasks := podman.TasksMax(toInt(ram), context)
				ram += "G"
				sh.C("systemctl", "set-property", slice, "MemoryHigh=infinity", "MemoryMax="+ram).Run()
				sh.C("systemctl", "set-property", slice, fmt.Sprintf("TasksMax=%d", tasks)).Run()
				fmt.Printf("- Tasks:      [OK]   Ceiling set to %d tasks (derived from RAM; /home/%s/TasksMax overrides).\n", tasks, context)
			}
			// no swap even on unlimited plans, a container at its memory limit gets oom-killed and restarted instead of swapping and saturating the disk for every account
			sh.C("systemctl", "set-property", slice, "MemorySwapMax=0").Run()
			fmt.Println("- Memory:     [OK]   " + ramText)
		}

		if !partial || docpu {
			if toInt(cpu) == 0 && !strings.Contains(cpu, ".") {
				pct, _, _ := podman.UnlimitedSliceCeiling()
				sh.C("systemctl", "set-property", slice, fmt.Sprintf("CPUQuota=%d%%", pct)).Run()
				fmt.Printf("- Ceiling:    [OK]   Unlimited CPU, safety ceiling at %d%%.\n", pct)
			} else {
				sh.C("systemctl", "set-property", slice, "CPUQuota="+applyCPUPercent(cpu)+"%").Run()
			}
			sh.C("systemctl", "set-property", slice, fmt.Sprintf("IOWeight=%d", podman.IOWeight(cpu))).Run()
			fmt.Println("- CPU:        [OK]   " + cpuText)
		}

		if !partial || dodsk {
			out, err := sh.C("setquota", "-u", context, blocks, blocks, inodes, inodes, "/").Combined()
			if err == nil {
				fmt.Println("- Disk        [OK]   " + diskText)
				fmt.Println("- Inodes:     [OK]   " + inodesText)
			} else {
				fmt.Printf("- Disk       [WARN]   Disk quota not applied: %s (filesystem may not support quotas).\n", out)
			}
			if !bulk {
				sh.Opencli("docker-collect_stats", username).Quiet().Background()
			}
		}

		if !partial || doemail {
			fmt.Println("- Emails      [OK]   " + hourlyText)
			if !bulk {
				sh.Opencli("email-ratelimit", "--username="+username).Quiet().Background()
			}
		}

		if !partial || donet {
			// bandwidth shaping used to nsenter into rootless dockerd's netns and apply tc there, no podman equivalent exists yet
			fmt.Println("- Bandwidth:[WARN]   Bandwidth limiting is not implemented yet under podman.")
		}
	}

	fmt.Println("+=============================================================================+")
	fmt.Println("Completed!")

	if bulk {
		sh.Opencli("docker-collect_stats", "--all").Quiet().Background()
		sh.Opencli("email-ratelimit", "--all-users").Quiet().Background()
		// cleanup of old progress logs
		filepath.WalkDir("/tmp", func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() || !strings.HasPrefix(d.Name(), "opencli_plan_apply_") {
				return nil
			}
			if info, e := d.Info(); e == nil && time.Since(info.ModTime()) >= 48*time.Hour {
				os.Remove(p)
			}
			return nil
		})
	}
	return 0
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ##############################################################################
// Script Name: internal/commands/user/change_plan.go
// Description: Change plan for a user and apply new plan limits.
// Usage: opencli user-change_plan <USERNAME> <NEW_PLAN_NAME> [--debug]
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

package user

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/diskquota"
	"opencli/internal/podman"
	"opencli/internal/redis"
	"opencli/internal/reseller"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-change_plan",
		Desc:  "Change plan for a user and apply new plan limits.",
		Usage: "opencli user-change_plan <USERNAME> <NEW_PLAN_NAME> [--debug]",
		Run:   changePlan,
	})
}

type cpCtx struct {
	username    string
	debug       bool
	context     string
	userID      string
	failures    int
	diskBlocks  int
	newDisk     string
	newInodes   string
	curPlanName string
	newPlanName string
	newPlanID   string
	curDisk     string
	owner       string
	slice       string
}

var cpDigits = regexp.MustCompile(`^[0-9]+$`)

// [[ "$v" -eq 0 ]], a non integer is an arithmetic error and counts as false
func cpIsZero(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n == 0
}

// echo "$v * 100" | bc
func cpBcTimes100(v string) string {
	intPart, frac, _ := strings.Cut(v, ".")
	digits := intPart + frac
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return ""
	}
	s := strconv.FormatInt(n*100, 10)
	if len(frac) == 0 {
		return s
	}
	for len(s) <= len(frac) {
		s = "0" + s
	}
	return s[:len(s)-len(frac)] + "." + s[len(s)-len(frac):]
}

func (c *cpCtx) checkReseller() {
	if c.owner == "" || c.owner == "root" {
		return
	}
	file := reseller.LimitsFile(c.owner)
	if !reseller.PlanAllowed(file, c.newPlanID) {
		cli.Fail("Error: Plan '%s' is not assigned to reseller '%s'.", c.newPlanName, c.owner)
	}
	oldDisk, newDisk := 0, 0
	if f := text.Field(c.curDisk, 1); cpDigits.MatchString(f) {
		oldDisk, _ = strconv.Atoi(f)
	}
	if f := text.Field(c.newDisk, 1); cpDigits.MatchString(f) {
		newDisk, _ = strconv.Atoi(f)
	}
	// 0 is unlimited, so it counts as bigger than anything
	bigger := oldDisk != 0 && (newDisk == 0 || newDisk > oldDisk)
	if bigger && reseller.OverDisk(file) {
		cli.Fail("Error: Reseller '%s' has reached the maximum disk usage limit, can't move user to a plan with more disk space.", c.owner)
	}
}

func (c *cpCtx) setProp(args ...string) {
	sh.C("systemctl", append([]string{"set-property", c.slice}, args...)...).Run()
}

func (c *cpCtx) updateResource(typ, value string) {
	upper := strings.ToUpper(typ)
	envValue := value
	if c.debug {
		fmt.Printf("Updating %s limit to %s\n", upper, value)
	}
	if typ == "ram" && !strings.HasSuffix(value, "G") {
		envValue = value + "G"
	}
	envFile := "/home/" + c.context + "/.env"
	if b, err := os.ReadFile(envFile); err != nil {
		cli.Errf("sed: can't read %s: No such file or directory", envFile)
	} else {
		lines, nl := splitLines(string(b))
		pre := "TOTAL_" + upper + "="
		for i, l := range lines {
			if strings.HasPrefix(l, pre) {
				lines[i] = pre + envValue
			}
		}
		conf.WriteFileKeep(envFile, joinLines(lines, nl), false)
	}

	if typ == "cpu" {
		if cpIsZero(value) {
			ceil, _, _ := podman.UnlimitedSliceCeiling()
			c.setProp(fmt.Sprintf("CPUQuota=%d%%", ceil))
			fmt.Printf("[✔] Unlimited CPU, safety ceiling set to %d%%.\n", ceil)
		} else {
			c.setProp("CPUQuota=" + cpBcTimes100(value) + "%")
		}
		c.setProp(fmt.Sprintf("IOWeight=%d", podman.IOWeight(value)))
	}
	if typ == "ram" {
		if cpIsZero(value) {
			_, high, max := podman.UnlimitedSliceCeiling()
			tasks := podman.TasksMax(max/1024, c.username)
			c.setProp(fmt.Sprintf("MemoryHigh=%dM", high), fmt.Sprintf("MemoryMax=%dM", max))
			c.setProp(fmt.Sprintf("TasksMax=%d", tasks))
			fmt.Printf("[✔] Unlimited RAM, safety ceiling set to %dMB (throttled) / %dMB (max), %d tasks.\n", high, max, tasks)
		} else {
			ramGB := value
			if !strings.HasSuffix(value, "G") {
				value += "G"
			}
			c.setProp("MemoryHigh=infinity", "MemoryMax="+value)
			n, _ := strconv.Atoi(ramGB)
			tasks := podman.TasksMax(n, c.username)
			c.setProp(fmt.Sprintf("TasksMax=%d", tasks))
			fmt.Printf("[✔] Tasks ceiling set to %d (derived from RAM; /home/%s/TasksMax overrides).\n", tasks, c.username)
		}
		// no swap even on unlimited plans, a container at its memory limit gets oom-killed and restarted instead of swapping and saturating the disk for every account
		c.setProp("MemorySwapMax=0")
	}
	fmt.Printf("[✔] Total %s limit (%s) set successfully.\n", upper, value)
}

func (c *cpCtx) updateDiskInodes() {
	if c.debug {
		fmt.Printf("Changing disk limit from: %s (%d blocks), inodes: %s\n", c.newDisk, c.diskBlocks, c.newInodes)
	}
	blocks := strconv.Itoa(c.diskBlocks)
	if _, err := diskquota.Set(c.username, blocks, c.newInodes); err == nil {
		fmt.Printf("[✔] Disk limit (%s) and inodes (%s) applied successfully.\n", c.newDisk, c.newInodes)
	} else {
		c.failures++
		fmt.Println("[✘] Error setting disk/inodes limits.")
		fmt.Printf("    %v\n", err)
	}
	sh.Opencli("user-quota").Quiet().Background()
}

func changePlan(args []string) int {
	if len(args) != 2 && len(args) != 3 {
		fmt.Println("Usage: opencli user-change_plan <username> <new_plan_name> [--debug]")
		return 1
	}
	c := &cpCtx{username: args[0], newPlanName: args[1], debug: cli.HasFlag(args, "--debug")}
	db.Require()

	row, _ := db.Cmd("SELECT u.plan_id, p.name, u.server, u.id, p.disk_limit, IFNULL(u.owner, '')\n        FROM users u\n        JOIN plans p ON p.id = u.plan_id\n        WHERE u.username = '"+db.Escape(c.username)+"'\n        LIMIT 1", "-N", "-B").Out()
	f := strings.FieldsFunc(strings.SplitN(row, "\n", 2)[0], func(r rune) bool { return r == '\t' })
	get := func(i int) string {
		if i < len(f) {
			return f[i]
		}
		return ""
	}
	curPlanID := get(0)
	c.curPlanName, c.context, c.userID, c.curDisk, c.owner = get(1), get(2), get(3), get(4), get(5)
	// panel cache keys use the db username, suspended prefix included
	dbUsername := c.username
	// if suspended, remove prefix
	if i := strings.LastIndex(c.username, "_"); i >= 0 {
		c.username = c.username[i+1:]
	}
	if curPlanID == "" {
		cli.Fail("Error: User '%s' not found in the database.", c.username)
	}

	prow, _ := db.Cmd("SELECT id, cpu, ram, disk_limit, inodes_limit, bandwidth\n        FROM plans\n        WHERE name = '"+db.Escape(c.newPlanName)+"'\n        LIMIT 1", "-N", "-B").Out()
	pf := strings.FieldsFunc(strings.SplitN(prow, "\n", 2)[0], func(r rune) bool { return r == '\t' })
	pget := func(i int) string {
		if i < len(pf) {
			return pf[i]
		}
		return ""
	}
	c.newPlanID = pget(0)
	cpu, ram := pget(1), pget(2)
	c.newDisk, c.newInodes = pget(3), pget(4)
	if c.newPlanID == "" {
		cli.Fail("Error: Plan '%s' not found in the database.", c.newPlanName)
	}

	c.checkReseller()

	numRam := strings.ReplaceAll(ram, "g", "")
	n, _ := strconv.Atoi(text.Field(c.newDisk, 1))
	c.diskBlocks = n * 1024000

	// podman has no ssh contexts anymore so only the local path is left
	uid := ""
	if st, err := os.Stat("/home/" + c.context); err == nil {
		if s, ok := st.Sys().(*syscall.Stat_t); ok {
			uid = strconv.Itoa(int(s.Uid))
		}
	}
	c.slice = "user-" + uid + ".slice"
	c.updateResource("cpu", cpu)
	c.updateResource("ram", numRam)
	// update_total_tc is todo upstream
	c.updateDiskInodes()

	if c.debug {
		fmt.Printf("Changing plan for user from '%s' to '%s'\n", c.curPlanName, c.newPlanName)
	}
	if db.Cmd("UPDATE users SET plan_id = "+c.newPlanID+" WHERE username = '"+db.Escape(c.username)+"';", "-N", "-B").Run() == nil {
		if c.failures > 0 {
			fmt.Printf("Plan changed successfully for user %s from %s to %s — (%d warnings)\n", c.username, c.curPlanName, c.newPlanName, c.failures)
		} else {
			fmt.Printf("Plan changed successfully for user %s from %s to %s\n", c.username, c.curPlanName, c.newPlanName)
		}
	} else {
		fmt.Println("Error: Could not update plan_id in database — is MariaDB running?")
	}
	// a plan change only affects the user's plan/feature-set lookups
	redis.DropUserCache(c.userID, dbUsername)
	return 0
}

// ##############################################################################
// Script Name: internal/commands/user/list.go
// Description: Display all users: id, username, email, plan, registered date,
// Usage: opencli user-list [--json] [--total] [--quota]
// Author: Stefan Pejcic
// Created: 16.10.2023
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
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/redis"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-list",
		Desc:  "Display all users: id, username, email, plan, registered date,",
		Usage: "opencli user-list [--json] [--total] [--quota]",
		Docs:  "https://docs.openpanel.com",
		Run:   list,
	})
}

const (
	listQuotaReport = "/etc/openpanel/openpanel/quota_report.json"
	listIPBase      = "/etc/openpanel/openpanel/core/users"
	listRed         = "\033[31m"
	listOrange      = "\033[38;5;208m"
	listGreen       = "\033[32m"
	listNC          = "\033[0m"
)

const listOnlineLua = `local c, out = "0", {}
repeat
    local r = redis.call("SCAN", c, "MATCH", ARGV[1], "COUNT", 1000)
    c = r[1]
    for _, k in ipairs(r[2]) do
        if redis.call("TYPE", k)["ok"] == "hash" then
            local u = redis.call("HGET", k, "username")
            if u then out[#out + 1] = u end
        end
    end
until c == "0"
return out`

const listSQL = `
        SELECT
            users.username,
            users.email,
            IF(users.owner IS NULL OR users.owner = '', 'root', users.owner) AS owner,
            users.server,
            users.registered_date,
            users.twofa_enabled,
            plans.name,
            plans.cpu,
            plans.ram,
            (SELECT COUNT(*) FROM domains d WHERE d.user_id = users.id) AS domains_count
        FROM users
        INNER JOIN plans ON users.plan_id = plans.id;
    `

type listCtx struct {
	json     bool
	useColor bool
	online   map[string]bool
	quota    map[string]string
}

func list(args []string) int {
	jsonOut, total := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--quota":
			sh.Opencli("user-quota").Run()
			return 0
		case "--json":
			jsonOut = true
		case "--total":
			total = true
		default:
			fmt.Println("Usage: opencli user-list [--json] [--total] [--quota]")
			return 1
		}
	}
	db.Require()

	if total {
		count, _ := db.Cmd("SELECT COUNT(*) FROM users", "-s").Timeout(10 * time.Second).Out()
		if jsonOut {
			fmt.Println(count)
		} else {
			fmt.Println("Total number of users: " + count)
		}
		return 0
	}
	l := &listCtx{json: jsonOut}
	l.useColor = isTTY(os.Stdout) && os.Getenv("NO_COLOR") == ""
	l.printUsers()
	return 0
}

// jq's @tsv escaping for a string
func listTsvEsc(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "\t", "\\t", "\n", "\\n", "\r", "\\r")
	return r.Replace(s)
}

// jq -r style value for @tsv, ok is false when jq would error out
func listTsvVal(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return listTsvEsc(t), true
	case json.Number:
		return t.String(), true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// jq: value // def, null and false count as absent
func listAlt(v any, def any) any {
	if v == nil {
		return def
	}
	if b, ok := v.(bool); ok && !b {
		return def
	}
	return v
}

// jq path .a.b.c, ok is false when it would error
func listPath(v any, keys ...string) (any, bool) {
	for _, k := range keys {
		switch t := v.(type) {
		case nil:
			return nil, true
		case *ojson.Object:
			v, _ = t.Get(k)
		default:
			return nil, false
		}
	}
	return v, true
}

func (l *listCtx) buildOnline() {
	l.online = map[string]bool{}
	out, _ := redis.CLI("EVAL", listOnlineLua, "0", "session:*").NoErr().Out()
	for _, u := range strings.Split(out, "\n") {
		if u != "" {
			l.online[u] = true
		}
	}
}

// one row per username, first one wins
func (l *listCtx) loadQuota() {
	l.quota = map[string]string{}
	if !conf.IsFile(listQuotaReport) {
		return
	}
	v, err := ojson.Parse(conf.Read(listQuotaReport))
	if err != nil {
		return
	}
	root, ok := v.(*ojson.Object)
	if !ok {
		return
	}
	us, _ := root.Get("users")
	var items []any
	switch t := us.(type) {
	case []any:
		items = t
	case *ojson.Object:
		for _, k := range t.Keys() {
			x, _ := t.Get(k)
			items = append(items, x)
		}
	}
	for _, it := range items {
		var o *ojson.Object
		switch t := it.(type) {
		case nil:
			continue
		case *ojson.Object:
			o = t
		default:
			return
		}
		un, _ := o.Get("username")
		us, ok := listTsvVal(un)
		if !ok {
			return
		}
		parts := []string{us}
		for _, k := range []string{"disk_used", "disk_hard", "inodes_used", "inodes_hard"} {
			x, _ := o.Get(k)
			s, ok := listTsvVal(listAlt(x, json.Number("0")))
			if !ok {
				return
			}
			parts = append(parts, s)
		}
		// read -r u rest with a tab IFS
		f := strings.FieldsFunc(strings.Join(parts, "\t"), func(r rune) bool { return r == '\t' })
		if len(f) == 0 || us == "" || f[0] != us {
			continue
		}
		if _, dup := l.quota[us]; !dup {
			l.quota[us] = strings.Join(f[1:], "\t")
		}
	}
}

func listHumanGB(v string) string {
	if i := strings.Index(v, "."); i >= 0 {
		v = v[:i]
	}
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(v) {
		v = "0"
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	if n*200%2048000 == 1024000 {
		return fmt.Sprintf("%.2fGB", float64(n)/1024000)
	}
	h := (n*200 + 1024000) / 2048000
	return fmt.Sprintf("%d.%02dGB", h/100, h%100)
}

func listDedicatedIP(plain string) string {
	f := listIPBase + "/" + plain + "/ip.json"
	if !conf.IsFile(f) {
		return ""
	}
	o, err := ojson.ParseObject(conf.Read(f))
	if err != nil {
		return ""
	}
	v, _ := o.Get("ip")
	v = listAlt(v, nil)
	if v == nil {
		return ""
	}
	return ojson.Text(v)
}

func listPct(v string) int {
	if i := strings.Index(v, "."); i >= 0 {
		v = v[:i]
	}
	n, err := strconv.Atoi(v)
	if err != nil || strings.HasPrefix(v, "+") {
		return 0
	}
	return n
}

func (l *listCtx) colorByPct(txt, pct string) string {
	p := listPct(pct)
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(strings.SplitN(pct, ".", 2)[0]) {
		p = 0
	}
	if l.useColor {
		if p >= 90 {
			txt = listRed + txt + listNC
		} else if p >= 80 {
			txt = listOrange + txt + listNC
		}
	}
	return txt
}

var listAnsi = regexp.MustCompile(`\033\[[0-9;]*m`)

func listTable(rows []string) {
	var widths []int
	split := func(r string) []string { return strings.Split(r, "\t") }
	for _, r := range rows {
		for i, c := range split(r) {
			n := utf8.RuneCountInString(listAnsi.ReplaceAllString(c, ""))
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if n > widths[i] {
				widths[i] = n
			}
		}
	}
	var b strings.Builder
	for _, r := range rows {
		cells := split(r)
		for i, c := range cells {
			if i == len(cells)-1 {
				b.WriteString(c + "\n")
			} else {
				n := utf8.RuneCountInString(listAnsi.ReplaceAllString(c, ""))
				b.WriteString(c + strings.Repeat(" ", widths[i]-n+2))
			}
		}
	}
	fmt.Print(b.String())
}

func listNum(s string, def string) any {
	if s == "" {
		s = def
	}
	if regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`).MatchString(s) {
		return json.Number(s)
	}
	return json.Number("0")
}

func (l *listCtx) printUsers() {
	rowsOut, _ := db.Cmd(listSQL).NoErr().Timeout(10 * time.Second).Out()
	rowsOut = strings.TrimRight(rowsOut, "\n")
	var rows string
	if i := strings.Index(rowsOut, "\n"); i >= 0 {
		rows = rowsOut[i+1:]
	}
	rows = strings.TrimRight(rows, "\n")
	if rows == "" {
		if l.json {
			fmt.Println(`{"data": [], "metadata": {"result": "ok"}}`)
		} else {
			fmt.Println("No users.")
		}
		return
	}
	l.buildOnline()
	l.loadQuota()
	serverIP := text.Field(sh.C("hostname", "-I").NoErr().Str(), 1)

	var objs []any
	table := []string{"USERNAME\tEMAIL\tSTATUS\tONLINE\t2FA\tIP ADDRESS\tPLAN\tDOMAINS\tRAM (used/allocated)\tCPU (used/allocated)\tDISK (used/allocated)\tINODES (used/allocated)\tNOTES"}
	for _, line := range strings.Split(rows, "\n") {
		f := strings.FieldsFunc(line, func(r rune) bool { return r == '\t' })
		g := func(i int) string {
			if i < len(f) {
				return f[i]
			}
			return ""
		}
		username, email, owner, context, registered, twofa := g(0), g(1), g(2), g(3), g(4), g(5)
		planName, cpu, ram, domains := g(6), g(7), g(8), g(9)
		if username == "" {
			continue
		}
		plain := username
		suspended := strings.Contains(username, "SUSPENDED_")
		if suspended {
			plain = username[strings.LastIndex(username, "_")+1:]
		}
		uid := "null"
		if out, err := sh.C("stat", "-c", "%u", "/home/"+context).NoErr().Out(); err == nil {
			uid = out
		}
		online := l.online[plain]
		twofaLabel := "Off"
		if twofa == "1" {
			twofaLabel = "On"
		}
		ipDed := listDedicatedIP(plain)
		ipKind, ipLabel := "shared", serverIP+" (shared)"
		if ipDed != "" {
			ipKind, ipLabel = "dedicated", ipDed+" (dedicated)"
		}
		notes := ""
		if b, err := os.ReadFile("/home/" + context + "/notes.txt"); err == nil {
			notes = strings.TrimRight(string(b), "\n")
		}
		ramAlloc, cpuAlloc := "Unlimited", "Unlimited"
		if ram != "0g" {
			ramAlloc = strings.TrimSuffix(ram, "g") + " GB"
		}
		if cpu != "0" {
			cpuAlloc = cpu + " Core"
		}
		ramUsed, cpuUsed := "N/A", "N/A"
		memUsedBytes, memTotalBytes, memPct, cpuPct := "0", "0", "0", "0"
		if !suspended {
			if tsv, ok := listUsage(context); ok {
				u := strings.FieldsFunc(tsv, func(r rune) bool { return r == '\t' })
				ug := func(i int) string {
					if i < len(u) {
						return u[i]
					}
					return ""
				}
				ramUsed, memPct, cpuUsed, cpuPct, memUsedBytes, memTotalBytes = ug(0), ug(1), ug(2), ug(3), ug(4), ug(5)
			}
		}
		diskUsed, diskHard, inodesUsed, inodesHard := "0GB", "unlimited", "0", "unlimited"
		diskUsedKB, diskHardKB, inodesUsedN, inodesHardN := "0", "0", "0", "0"
		if q := l.quota[plain]; q != "" {
			qf := strings.FieldsFunc(q, func(r rune) bool { return r == '\t' })
			qg := func(i int) string {
				if i < len(qf) {
					return qf[i]
				}
				return ""
			}
			diskUsedKB, diskHardKB, inodesUsedN, inodesHardN = qg(0), qg(1), qg(2), qg(3)
			diskUsed = listHumanGB(diskUsedKB)
			if diskHardKB != "0" {
				diskHard = listHumanGB(diskHardKB)
			}
			inodesUsed = inodesUsedN
			if inodesHardN != "0" {
				inodesHard = inodesHardN
			}
		}

		if l.json {
			ipAddr := ipDed
			if ipAddr == "" {
				ipAddr = serverIP
			}
			var id any
			if uid == "null" {
				id = nil
			} else {
				id = listNum(uid, "0")
			}
			usage := ojson.New().
				Set("ram", ojson.New().Set("used", ramUsed).Set("allocated", ramAlloc).Set("usage_pct", listNum(memPct, "0")).Set("used_bytes", listNum(memUsedBytes, "0")).Set("total_bytes", listNum(memTotalBytes, "0"))).
				Set("cpu", ojson.New().Set("used", cpuUsed).Set("allocated", cpuAlloc).Set("usage_pct", listNum(cpuPct, "0"))).
				Set("disk", ojson.New().Set("used", diskUsed).Set("allocated", diskHard).Set("used_kb", listNum(diskUsedKB, "0")).Set("hard_kb", listNum(diskHardKB, "0"))).
				Set("inodes", ojson.New().Set("used", listNum(inodesUsed, "0")).Set("allocated", inodesHard))
			o := ojson.New().
				Set("id", id).
				Set("username", username).
				Set("context", context).
				Set("owner", owner).
				Set("package", ojson.New().Set("name", planName).Set("owner", owner)).
				Set("email", email).
				Set("locale_code", "EN_us").
				Set("registered_date", registered).
				Set("suspended", suspended).
				Set("online", online).
				Set("twofa_enabled", twofa == "1").
				Set("ip", ojson.New().Set("kind", ipKind).Set("address", ipAddr)).
				Set("domains", listNum(domains, "0")).
				Set("usage", usage).
				Set("notes", notes)
			objs = append(objs, o)
			continue
		}

		statusLabel := "Active"
		if suspended {
			statusLabel = "Suspended"
			if l.useColor {
				statusLabel = listRed + "Suspended" + listNC
			}
		}
		onlineLabel := "No"
		if online {
			onlineLabel = "Yes"
			if l.useColor {
				onlineLabel = listGreen + "●" + listNC + " Yes"
			}
		}
		notesDisplay := strings.ReplaceAll(notes, "\n", " ")
		// unlimited allocations stay at 0% so they never get colored
		ramPct, cpuAllocPct, diskPct, inodesPct := "0", "0", "0", "0"
		if ramAlloc != "Unlimited" {
			ramPct = memPct
		}
		if cpuAlloc != "Unlimited" {
			cpuAllocPct = cpuPct
		}
		if hard := listPct(diskHardKB); hard > 0 {
			diskPct = strconv.Itoa(listPct(diskUsedKB) * 100 / hard)
		}
		if hard := listPct(inodesHardN); hard > 0 {
			inodesPct = strconv.Itoa(listPct(inodesUsedN) * 100 / hard)
		}
		d := domains
		if d == "" {
			d = "0"
		}
		table = append(table, strings.Join([]string{
			plain, email, statusLabel, onlineLabel, twofaLabel, ipLabel, planName, d,
			l.colorByPct(ramUsed+"/"+ramAlloc, ramPct),
			l.colorByPct(cpuUsed+"/"+cpuAlloc, cpuAllocPct),
			l.colorByPct(diskUsed+"/"+diskHard, diskPct),
			l.colorByPct(inodesUsed+"/"+inodesHard, inodesPct),
			notesDisplay,
		}, "\t"))
	}
	if l.json {
		res := ojson.New().Set("data", append([]any{}, objs...)).Set("metadata", ojson.New().Set("result", "ok"))
		fmt.Println(ojson.Pretty(res))
		return
	}
	listTable(table)
}

// last json line of resource_usage.txt as the six tab separated fields jq builds, ok is false when jq prints nothing
func listUsage(context string) (string, bool) {
	b, err := os.ReadFile("/home/" + context + "/resource_usage.txt")
	if err != nil {
		return "", false
	}
	s := string(b)
	if s == "" {
		return "", false
	}
	s = strings.TrimSuffix(s, "\n")
	line := s[strings.LastIndex(s, "\n")+1:]
	line = strings.TrimRight(line, "\n")
	if line == "" {
		return "", false
	}
	v, err := ojson.Parse(line)
	if err != nil || v == nil {
		return "", false
	}
	if b, ok := v.(bool); ok && !b {
		return "", false
	}
	var parts []string
	for _, spec := range []struct {
		path []string
		def  any
	}{
		{[]string{"memory", "used", "human"}, "N/A"},
		{[]string{"memory", "usage_pct"}, json.Number("0")},
		{[]string{"cpu", "usage", "human"}, "N/A"},
		{[]string{"cpu", "usage", "pct"}, json.Number("0")},
		{[]string{"memory", "used", "bytes"}, json.Number("0")},
		{[]string{"memory", "total", "bytes"}, json.Number("0")},
	} {
		x, ok := listPath(v, spec.path...)
		if !ok {
			return "", false
		}
		t, ok := listTsvVal(listAlt(x, spec.def))
		if !ok {
			return "", false
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, "\t"), true
}

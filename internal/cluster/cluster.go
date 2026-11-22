// ##############################################################################
// Script Name: internal/cluster/cluster.go
// Description: Multi-server helpers for the dispatcher: lists from every server, routing to the account's server, --server= and --all-servers.
// Usage: used by the opencli dispatcher
// Author: Stefan Pejcic
// Created: 20.11.2026
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

package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/sh"
)

const state = "/etc/openpanel/openadmin/cluster/cluster.json"

func set(names string) map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(names) {
		m[n] = true
	}
	return m
}

var (
	// ListCmds list from every server unless --local is passed
	ListCmds = set("user-list domains-all websites-all plan-list ftp-list email-manage")
	// UserCmds run on the server that has the account
	UserCmds = set("domains-user websites-user user-suspend user-unsuspend user-password user-change_plan user-quota user-2fa user-email user-login user-delete user-litespeed php-default")
	// DomainCmds run on the server that has the domain
	DomainCmds = set("domains-whoowns domains-suspend domains-unsuspend domains-delete domains-ssl domains-dns domains-docroot domains-update_ns")
	// AllCmds take --all-servers
	AllCmds = set("config waf plan-create plan-edit plan-delete domains-update_ns license update sentinel version imunify api email-server")
	// ServerCmds work with --server=, same list the slaves accept
	ServerCmds = func() map[string]bool {
		m := set("user-add domains-add")
		for _, s := range []map[string]bool{ListCmds, UserCmds, DomainCmds, AllCmds} {
			for k := range s {
				m[k] = true
			}
		}
		return m
	}()
)

// Bin is the openadmin binary that knows how to reach the slaves
func Bin() string {
	for _, b := range []string{"/usr/local/admin/openadmin-amd64", "/usr/local/admin/openadmin-arm64"} {
		if st, err := os.Stat(b); err == nil && st.Mode()&0o111 != 0 {
			return b
		}
	}
	return ""
}

var masterRe = regexp.MustCompile(`"role": *"master"`)

// Slaves lists live slaves, nothing when this isn't a master with slaves
func Slaves() string {
	if os.Getenv("OPENCLI_CLUSTER_LOCAL") == "1" {
		return ""
	}
	b, err := os.ReadFile(state)
	if err != nil || !masterRe.Match(b) {
		return ""
	}
	bin := Bin()
	if bin == "" {
		return ""
	}
	return sh.C(bin, "cluster-exec", "servers").NoErr().Str()
}

// Self is this server's hostname
func Self() string {
	h, _ := os.Hostname()
	return h
}

// IsSelf is true when name is this server by full or short hostname
func IsSelf(name string) bool {
	self := strings.ToLower(Self())
	n := strings.ToLower(name)
	short, _, _ := strings.Cut(self, ".")
	return n == self || n == short
}

// Header prints the per-server separator
func Header(name string) { fmt.Println("── " + name + " ──") }

func execJSON(target string, args ...string) (string, error) {
	return sh.C(Bin(), append([]string{"cluster-exec", "run", target, "--"}, args...)...).Out()
}

type result struct {
	Server string  `json:"server"`
	Out    string  `json:"out"`
	Error  *string `json:"error"`
	RC     int     `json:"rc"`
}

// RunRemote runs opencli on slaves and prints each server's output under its name, returns the worst exit code
func RunRemote(target string, args ...string) int {
	var errb strings.Builder
	out, err := sh.C(Bin(), append([]string{"cluster-exec", "run", target, "--"}, args...)...).Stderr(&errb).Out()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: "+strings.TrimRight(errb.String(), "\n"))
		return 1
	}
	var rows []result
	json.Unmarshal([]byte(out), &rows)
	rc := 0
	for _, r := range rows {
		Header(r.Server)
		if r.Out != "" {
			fmt.Println(strings.TrimRight(r.Out, "\n"))
		}
		if r.Error != nil && *r.Error != "" {
			fmt.Fprintln(os.Stderr, "Error: "+*r.Error)
		}
		if r.RC > rc {
			rc = r.RC
		}
	}
	return rc
}

// mergeJSON joins json from all servers: arrays and {"data": ...} get a server field, numbers are summed
func mergeJSON(parts [][2]string) string {
	tag := func(v any, name string) any {
		if o, ok := v.(*ojson.Object); ok {
			n := ojson.New().Set("server", name)
			for _, k := range o.Keys() {
				if k == "server" {
					continue
				}
				x, _ := o.Get(k)
				n.Set(k, x)
			}
			return n
		}
		return v
	}
	var items []any
	var dataList []any
	var dataDict *ojson.Object
	var total *float64
	var base *ojson.Object
	type kv struct{ k, v string }
	var other []kv
	addOther := func(name, v string) {
		for i := range other {
			if other[i].k == name {
				other[i].v = v
				return
			}
		}
		other = append(other, kv{name, v})
	}
	for _, p := range parts {
		name, raw := p[0], p[1]
		v, err := ojson.Parse(raw)
		if err != nil {
			if strings.TrimSpace(raw) != "" {
				addOther(name, strings.TrimSpace(raw))
			}
			continue
		}
		switch t := v.(type) {
		case json.Number:
			f, _ := t.Float64()
			if total == nil {
				total = new(float64)
			}
			*total += f
		case []any:
			for _, i := range t {
				items = append(items, tag(i, name))
			}
		case *ojson.Object:
			d, _ := t.Get("data")
			switch dv := d.(type) {
			case []any:
				if base == nil {
					base = t
				}
				if dataList == nil {
					dataList = []any{}
				}
				for _, i := range dv {
					dataList = append(dataList, tag(i, name))
				}
			case *ojson.Object:
				if base == nil {
					base = t
				}
				if dataDict == nil {
					dataDict = ojson.New()
				}
				for _, k := range dv.Keys() {
					x, _ := dv.Get(k)
					dataDict.Set(k, tag(x, name))
				}
			default:
				addOther(name, pyRepr(t))
			}
		default:
			addOther(name, pyRepr(t))
		}
	}
	var out any
	switch {
	case dataList != nil || dataDict != nil:
		var data any = dataDict
		if dataList != nil {
			data = dataList
		}
		// an empty list from a server with nothing on it shouldn't turn a dict into a list
		if dataList != nil && dataDict != nil {
			if len(dataList) == 0 {
				data = dataDict
			} else {
				data = dataList
			}
		}
		o := ojson.New()
		for _, k := range base.Keys() {
			x, _ := base.Get(k)
			o.Set(k, x)
		}
		o.Set("data", data)
		out = o
	case total != nil && len(items) == 0:
		out = pyNumber(*total)
	case len(items) > 0 || len(other) == 0:
		if items == nil {
			items = []any{}
		}
		out = items
	default:
		out = ojson.New()
	}
	if len(other) > 0 {
		var lines []string
		for _, o := range other {
			lines = append(lines, o.k+": "+o.v)
		}
		fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
	}
	return pyDumps(out)
}

func pyNumber(f float64) any {
	if f == float64(int64(f)) {
		return json.Number(fmt.Sprint(int64(f)))
	}
	return json.Number(fmt.Sprint(f))
}

func pyRepr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return ojson.Compact(v)
}

// pyDumps matches python's json.dumps(indent=2), which escapes non-ascii
func pyDumps(v any) string {
	s := ojson.Pretty(v)
	var b strings.Builder
	for _, r := range s {
		if r > 127 {
			if r > 0xffff {
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// List runs a list command here and on every slave, json is merged, text gets a header per server
func List(command string, args []string) int {
	jsonMode := false
	for _, a := range args {
		if a == "--json" {
			jsonMode = true
		}
	}
	local := func() *sh.Cmd { return sh.C(sh.Self(), append([]string{command}, args...)...).Env("OPENCLI_RAW=1") }
	rc := 0
	if !jsonMode {
		Header(Self())
		if code := local().Code(); code != 0 {
			rc = code
		}
		if code := RunRemote("all", append([]string{command}, args...)...); code != 0 {
			rc = code
		}
		return rc
	}
	localOut, err := local().Out()
	if err != nil {
		rc = sh.Code(err)
	}
	remoteRaw, err := execJSON("all", append([]string{command}, args...)...)
	var remote []result
	if err != nil || json.Unmarshal([]byte(remoteRaw), &remote) != nil {
		remote = nil
	}
	parts := [][2]string{{Self(), localOut}}
	for _, r := range remote {
		if r.Error == nil {
			parts = append(parts, [2]string{r.Server, r.Out})
		}
	}
	fmt.Println(mergeJSON(parts))
	for _, r := range remote {
		if r.Error != nil {
			fmt.Fprintf(os.Stderr, "Error: %s: %s\n", r.Server, *r.Error)
		}
	}
	return rc
}

func countLocal(sql string) bool {
	if _, err := os.Stat(db.ConfigFile); err != nil {
		fmt.Println("FATAL ERROR: MariaDB config file not available: " + db.ConfigFile)
		os.Exit(1)
	}
	out, err := db.Cmd(sql, "-s").Timeout(10 * time.Second).NoErr().Out()
	_ = out
	if err != nil {
		return false
	}
	var n int
	fmt.Sscan(out, &n)
	return n > 0
}

// Subject picks the account or domain a routed command acts on
func Subject(command string, args []string) string {
	var positional []string
	prev := ""
	for _, a := range args {
		if command == "user-quota" && prev == "--update" {
			return a
		}
		prev = a
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	switch command {
	case "user-quota":
		return ""
	case "domains-dns":
		if len(positional) > 1 {
			return positional[1]
		}
		return ""
	}
	if len(positional) > 0 {
		return positional[0]
	}
	return ""
}

// Owner returns the slave that has the account or domain, "" when it's here or nowhere
func Owner(kind, value string) string {
	if value == "" {
		return ""
	}
	v := db.Escape(value)
	if kind == "user" {
		if countLocal("SELECT COUNT(*) FROM users WHERE username='" + v + "' OR username LIKE 'SUSPENDED\\_%\\_" + v + "'") {
			return ""
		}
	} else if countLocal("SELECT COUNT(*) FROM domains WHERE domain_url='" + v + "'") {
		return ""
	}
	names := sh.C(Bin(), "cluster-exec", "find", kind, value).NoErr().Str()
	var found []string
	for _, n := range strings.Split(names, "\n") {
		if n != "" {
			found = append(found, n)
		}
	}
	if len(found) == 1 {
		return names
	}
	return ""
}

// PlanEditAll sends plan-edit to each slave with that slave's id for the same plan name
func PlanEditAll(planName string, args []string) int {
	rc := 0
	for _, server := range strings.Split(Slaves(), "\n") {
		if server == "" {
			continue
		}
		remoteID := ""
		raw, _ := execJSON(server, "plan-list", "--json")
		var rows []result
		json.Unmarshal([]byte(raw), &rows)
		if len(rows) > 0 {
			if v, err := ojson.Parse(rows[0].Out); err == nil {
				if arr, ok := v.([]any); ok {
					for _, it := range arr {
						if o, ok := it.(*ojson.Object); ok && o.Str("name") == planName {
							remoteID = o.Str("id")
							break
						}
					}
				}
			}
		}
		if remoteID == "" {
			Header(server)
			fmt.Printf("Plan '%s' doesn't exist here, skipped.\n", planName)
			continue
		}
		var out []string
		for _, a := range args {
			if strings.HasPrefix(a, "id=") {
				a = "id=" + remoteID
			}
			out = append(out, a)
		}
		if code := RunRemote(server, append([]string{"plan-edit"}, out...)...); code != 0 {
			rc = code
		}
	}
	return rc
}

// PlanNameByID looks up a plan's name for plan-edit --all-servers
func PlanNameByID(id string) string {
	if _, err := os.Stat(db.ConfigFile); err != nil {
		fmt.Println("FATAL ERROR: MariaDB config file not available: " + db.ConfigFile)
		os.Exit(1)
	}
	return db.Cmd("SELECT name FROM plans WHERE id='"+db.Escape(id)+"'", "-s").Timeout(10 * time.Second).NoErr().Str()
}

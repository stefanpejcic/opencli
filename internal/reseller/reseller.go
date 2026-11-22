// ##############################################################################
// Script Name: internal/reseller/reseller.go
// Description: Reseller limit files, allowed plans and logo helpers.
// Usage: imported by command packages
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

package reseller

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"opencli/internal/ojson"
	"opencli/internal/sh"
)

// Paths used by the reseller helpers
const (
	Dir      = "/etc/openpanel/openadmin/resellers"
	Template = "/etc/openpanel/openadmin/config/reseller_template.json"
	PlansDir = "/etc/openpanel/openadmin/reseller_plans"
)

// LimitsFile returns the reseller's limits file, creating it from the template when missing
func LimitsFile(reseller string) string {
	file := Dir + "/" + reseller + ".json"
	if _, err := os.Stat(file); err != nil {
		os.MkdirAll(Dir, 0o755)
		if b, err := os.ReadFile(Template); err == nil {
			os.WriteFile(file, b, 0o644)
		} else {
			os.WriteFile(file, []byte(`{"max_accounts":0,"current_accounts":0,"allowed_plans":[],"max_disk_blocks":0,"current_disk_blocks":0}`+"\n"), 0o644)
		}
	}
	return file
}

// Load reads a json object file keeping key order
func Load(file string) (*ojson.Object, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return ojson.ParseObject(string(b))
}

// toNumber is jq's `tonumber? // empty`
func toNumber(v any) (json.Number, bool) {
	s := strings.TrimSpace(ojson.Text(v))
	if _, err := strconv.ParseFloat(s, 64); err != nil || s == "" {
		return "", false
	}
	if f, _ := strconv.ParseFloat(s, 64); f == float64(int64(f)) {
		return json.Number(strconv.FormatInt(int64(f), 10)), true
	}
	return json.Number(s), true
}

// PlansJSON turns "1,2" or ["1",2] into a compact json array of numbers
func PlansJSON(input string) string {
	var items []any
	if strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]") {
		v, err := ojson.Parse(input)
		if arr, ok := v.([]any); err == nil && ok {
			items = arr
		}
	} else {
		for _, p := range strings.Split(input, ",") {
			items = append(items, p)
		}
	}
	out := []any{}
	for _, it := range items {
		if n, ok := toNumber(it); ok {
			out = append(out, n)
		}
	}
	return ojson.Compact(out)
}

func numList(o *ojson.Object, key string) []string {
	v, _ := o.Get(key)
	arr, _ := v.([]any)
	var out []string
	for _, it := range arr {
		out = append(out, ojson.Text(it))
	}
	return out
}

// PlanAllowed reports whether plan id is in allowed_plans, ids may be strings or numbers
func PlanAllowed(file, planID string) bool {
	o, err := Load(file)
	if err != nil {
		return false
	}
	for _, id := range numList(o, "allowed_plans") {
		if id == planID {
			return true
		}
	}
	return false
}

func num(o *ojson.Object, key string) float64 {
	v, ok := o.Get(key)
	if !ok || v == nil {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(ojson.Text(v)), 64)
	if err != nil {
		return 0
	}
	return f
}

// OverDisk reports whether the reseller is at or over max_disk_blocks, 0 is unlimited
func OverDisk(file string) bool {
	o, err := Load(file)
	if err != nil {
		return false
	}
	max := num(o, "max_disk_blocks")
	return max > 0 && num(o, "current_disk_blocks") >= max
}

var planName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// PlanFile returns the reseller plan's file, "" when the name is invalid or the plan doesn't exist
func PlanFile(name string) string {
	if !planName.MatchString(name) {
		return ""
	}
	file := PlansDir + "/" + name + ".json"
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	if _, err := ojson.Parse(string(b)); err != nil {
		return ""
	}
	return file
}

// OwnPlans returns the plan ids the reseller made themselves as a compact json array
func OwnPlans(file string) string {
	o, err := Load(file)
	if err != nil {
		return "[]"
	}
	nums := func(obj *ojson.Object, key string) []any {
		v, _ := obj.Get(key)
		arr, _ := v.([]any)
		out := []any{}
		for _, it := range arr {
			if n, ok := toNumber(it); ok {
				out = append(out, n)
			}
		}
		return out
	}
	if o.Has("own_plans") {
		return ojson.Compact(nums(o, "own_plans"))
	}
	// older files don't track it, so anything allowed beyond the current reseller plan counts as their own
	current := o.Str("reseller_plan")
	pkg := ""
	if current != "" {
		pkg = PlanFile(current)
	}
	if pkg == "" {
		return "[]"
	}
	p, err := Load(pkg)
	if err != nil {
		return "[]"
	}
	drop := map[string]bool{}
	for _, n := range nums(p, "allowed_plans") {
		drop[ojson.Text(n)] = true
	}
	out := []any{}
	for _, n := range nums(o, "allowed_plans") {
		if !drop[ojson.Text(n)] {
			out = append(out, n)
		}
	}
	return ojson.Compact(out)
}

var logoURL = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/?#@!&()*+,;=%\[-]+$`)

// LogoURLValid only allows plain http(s) urls since they end up in every owned account's .env
func LogoURLValid(url string) bool {
	if url == "" {
		return true
	}
	return len(url) <= 2048 && logoURL.MatchString(url)
}

const setLogo = `
        f="$1"; v="$2"
        [[ -f "$f" ]] || exit 1
        t=$(mktemp "$f.XXXXXX") || exit 1
        if { grep -v "^RESELLER_LOGO_URL=" "$f"; printf "RESELLER_LOGO_URL=\"%s\"\n" "$v"; } > "$t" && cat "$t" > "$f"; then
            rm -f "$t"; exit 0
        fi
        rm -f "$t"; exit 1
    `

// EnvSetLogo writes RESELLER_LOGO_URL into an account's .env as the account's own uid, so a planted symlink can't reach root files
func EnvSetLogo(envFile, url string) bool {
	st, err := os.Stat(dirOf(envFile))
	if err != nil {
		return false
	}
	s := st.Sys().(*syscall.Stat_t)
	if s.Uid < 1000 {
		return false
	}
	return sh.C("setpriv", "--reuid="+strconv.Itoa(int(s.Uid)), "--regid="+strconv.Itoa(int(s.Gid)), "--clear-groups", "--",
		"bash", "-c", setLogo, "_", envFile, url).OK()
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "."
}

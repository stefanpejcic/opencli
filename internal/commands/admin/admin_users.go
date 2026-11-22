// ##############################################################################
// Script Name: internal/commands/admin/admin_users.go
// Description: Manage OpenAdmin service and Administrators.
// Usage: opencli admin <command> [options]
// Author: Stefan Pejcic
// Created: 01.11.2023
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

package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
	"opencli/internal/ojson"
	"opencli/internal/reseller"
	"opencli/internal/sh"
	"opencli/internal/text"
)

func get4(url string) (string, bool) {
	d := &net.Dialer{}
	c := &http.Client{Timeout: time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", addr)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err == nil
}

var ipv4Re = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)

func publicIP() string {
	ip, ok := get4("https://ip.openpanel.com")
	if !ok {
		ip2, _ := get4("https://ifconfig.me/ip")
		ip += ip2
	}
	ip = strings.TrimRight(ip, "\n")
	if ip != "" && ipv4Re.MatchString(ip) {
		return ip
	}
	// hostname -I | awk '{print $1}'
	ifaces, _ := net.Interfaces()
	var v4, v6 []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() {
				continue
			}
			if n.IP.To4() != nil {
				v4 = append(v4, n.IP.String())
			} else {
				v6 = append(v6, n.IP.String())
			}
		}
	}
	if all := append(v4, v6...); len(all) > 0 {
		return all[0]
	}
	return ""
}

func hash(password string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return sh.C("openssl", "passwd", "-6", "-salt", hex.EncodeToString(b), "-stdin").Stdin(password + "\n").Str()
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{5,30}$`)
var passRe = regexp.MustCompile(`^[^[:space:]]{6,30}$`)

// only letters and numbers allowed, added this check in 0.2.8
func validateName(input, field string) {
	// https://github.com/stefanpejcic/OpenPanel/issues/442
	if strings.HasPrefix(input, "$$") {
		fmt.Println("ERROR: " + field + " cannot start with '$$' as it may cause unintended behavior.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openadmin")
		cli.Exit(1)
	}
	if !nameRe.MatchString(input) {
		fmt.Println("ERROR: " + field + " is invalid. It must contain only letters and numbers, and be between 5 and 30 characters.")
		fmt.Println("       docs: https://openpanel.com/docs/articles/accounts/forbidden-usernames/#openadmin")
		cli.Exit(1)
	}
}

// same rule as OpenAdmin, so a password set from the UI or API also works here
func validatePassword(input string) {
	if strings.HasPrefix(input, "$$") {
		fmt.Println("ERROR: Password cannot start with '$$' as it may cause unintended behavior.")
		fmt.Println("       docs: https://openpanel.com/docs/opencli/admin/#create-new-admin")
		cli.Exit(1)
	}
	if !passRe.MatchString(input) {
		fmt.Println("ERROR: Password is invalid. It must be between 6 and 30 characters and can't contain spaces.")
		fmt.Println("       docs: https://openpanel.com/docs/opencli/admin/#create-new-admin")
		cli.Exit(1)
	}
}

func communityExit(msg string) {
	fmt.Println(red + "Error" + reset + ": " + msg)
	fmt.Println(enterpriseLink)
	cli.Exit(1)
}

func newUser(username, password, flag, planArg string) {
	plan := ""
	if p, ok := strings.CutPrefix(planArg, "--plan="); ok {
		plan = p
	}
	if username == "" || password == "" {
		fmt.Println("ERROR: Invalid 'opencli admin new' command - please provide username and password.")
		usage()
	}
	if flag == "--reseller" {
		if licenseKey() == "" {
			communityExit("The OpenPanel Community Edition does not support Reseller users. To create multiple Administrator and Reseller accounts, please upgrade to the Enterprise Edition.")
		}
		if plan != "" && reseller.PlanFile(plan) == "" {
			fmt.Fprintln(os.Stderr, red+"Error"+reset+": Reseller plan '"+plan+"' not found.")
			cli.Exit(1)
		}
	}
	validateName(username, "Username")
	validatePassword(password)

	pwHash := hash(password)
	// check total user count and license
	if licenseKey() == "" && text.Atoi(sq("SELECT COUNT(*) FROM user;")) >= 1 {
		communityExit("The OpenPanel Community Edition supports only a single Admin user. To enable multiple Administrator and Reseller accounts, please upgrade to the Enterprise Edition.")
	}
	if text.Atoi(sq("SELECT COUNT(*) FROM user WHERE username='"+esc(username)+"';")) > 0 {
		fmt.Println(red + "Error" + reset + ": Username '" + username + "' already exists.")
		cli.Exit(1)
	}
	role := "user"
	switch flag {
	case "--reseller":
		role = "reseller"
	case "--super":
		// cluster_<name> accounts are made by a multi-server master to log its admins in, they don't count
		if text.Atoi(sq(`SELECT COUNT(*) FROM user WHERE role = 'admin' AND username NOT LIKE 'cluster\_%' ESCAPE '\';`)) != 0 {
			fmt.Println("Super Admin user already exists. Cannot create another super admin.")
			cli.Exit(1)
		}
		role = "admin"
	}
	insert := "INSERT INTO user (username, password_hash, role) VALUES ('" + esc(username) + "', '" + esc(pwHash) + "', '" + role + "');"
	create := "CREATE TABLE IF NOT EXISTS user (id INTEGER PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user', is_active BOOLEAN DEFAULT 1 NOT NULL, totp_secret TEXT, totp_enabled BOOLEAN DEFAULT 0 NOT NULL);"
	if out, err := sh.C("sqlite3", dbFile, create, insert).Combined(); err != nil {
		fmt.Println("User not created: " + out)
		return
	}
	switch flag {
	case "--reseller":
		os.Remove(reseller.Dir + "/" + username + ".json")
		reseller.LimitsFile(username)
		fmt.Printf("Reseller user '%s' created.\n", username)
		if plan != "" {
			updateReseller([]string{username, "--plan=" + plan})
		}
		sentinel("reseller_create", "Reseller created", "Reseller administrator account '"+username+"' has been created.")
	case "--super":
		fmt.Printf("Super Administrator '%s' created.\n", username)
	default:
		fmt.Printf("Admin User '%s' created.\n", username)
		sentinel("admin_create", "Administrator created", "Administrator account '"+username+"' has been created.")
	}
}

func toNum(v any) (json.Number, bool) {
	s := strings.TrimSpace(ojson.Text(v))
	if _, err := strconv.ParseFloat(s, 64); err != nil || s == "" {
		return "", false
	}
	return json.Number(ojson.Compact(json.Number(s))), true
}

func numArr(v any) []any {
	arr, _ := v.([]any)
	out := []any{}
	for _, it := range arr {
		if n, ok := toNum(it); ok {
			out = append(out, n)
		}
	}
	return out
}

// jq unique on a list of numbers
func uniqueNums(items []any) []any {
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := strconv.ParseFloat(ojson.Text(items[i]), 64)
		b, _ := strconv.ParseFloat(ojson.Text(items[j]), 64)
		return a < b
	})
	out := []any{}
	for i, it := range items {
		if i > 0 {
			a, _ := strconv.ParseFloat(ojson.Text(it), 64)
			b, _ := strconv.ParseFloat(ojson.Text(items[i-1]), 64)
			if a == b {
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

// jq -r '.k // 0' on a plan file
func planVal(o *ojson.Object, k string) string {
	v, _ := o.Get(k)
	if v == nil || v == false {
		return "0"
	}
	return ojson.Text(v)
}

func updateReseller(args []string) bool {
	username, allowed, maxAcc, maxDisk, logo, plan := "", "", "", "", "", ""
	logoSet := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--plan="):
			plan = a[strings.Index(a, "=")+1:]
		case strings.HasPrefix(a, "--allowed_plans="):
			allowed = a[strings.Index(a, "=")+1:]
		case strings.HasPrefix(a, "--max_accounts="):
			maxAcc = a[strings.Index(a, "=")+1:]
		case strings.HasPrefix(a, "--max_disk_blocks="):
			maxDisk = a[strings.Index(a, "=")+1:]
		case strings.HasPrefix(a, "--logo_url="):
			logo, logoSet = a[strings.Index(a, "=")+1:], true
		default:
			if username == "" {
				username = a
			}
		}
	}
	if logoSet && !reseller.LogoURLValid(logo) {
		fmt.Fprintln(os.Stderr, "Error: Invalid --logo_url, use a plain http:// or https:// URL without quotes, spaces, $, ` or \\.")
		return false
	}
	if username == "" || !strings.Contains(sq("SELECT 1 FROM user WHERE username='"+esc(username)+"' AND role='reseller';"), "1") {
		fmt.Fprintf(os.Stderr, "Error: Reseller '%s' does not exist.\n", username)
		return false
	}
	file := reseller.LimitsFile(username)

	own := ""
	if allowed != "" {
		allowed = reseller.PlansJSON(allowed)
		av, _ := ojson.Parse(allowed)
		al, _ := av.([]any)
		ov, _ := ojson.Parse(reseller.OwnPlans(file))
		kept := []any{}
		ownArr, _ := ov.([]any)
		// plans the admin removed by hand stop being the reseller's own
		for _, p := range ownArr {
			for _, x := range al {
				if ojson.Compact(x) == ojson.Compact(p) {
					kept = append(kept, p)
					break
				}
			}
		}
		own = ojson.Compact(kept)
	}
	if plan != "" {
		planFile := reseller.PlanFile(plan)
		if planFile == "" {
			fmt.Fprintf(os.Stderr, "Error: Reseller plan '%s' not found.\n", plan)
			return false
		}
		po, _ := reseller.Load(planFile)
		if po == nil {
			po = ojson.New()
		}
		// explicit flags win over the plan's values
		if maxAcc == "" {
			maxAcc = planVal(po, "max_accounts")
		}
		if maxDisk == "" {
			maxDisk = planVal(po, "max_disk_blocks")
		}
		// keep the plans the reseller created, the plan file only lists the admin's ones
		if allowed == "" {
			own = reseller.OwnPlans(file)
			ov, _ := ojson.Parse(own)
			ownArr, _ := ov.([]any)
			pa, _ := po.Get("allowed_plans")
			allowed = ojson.Compact(uniqueNums(append(numArr(pa), ownArr...)))
		}
		// refuse a downgrade below what the reseller already uses
		db.Require()
		cur := db.Cmd("SELECT COUNT(*) FROM users WHERE owner='"+db.Escape(username)+"';", "-N", "-B").NoErr().Str()
		disk := "0"
		if ro, err := reseller.Load(file); err == nil {
			if v, ok := ro.Get("current_disk_blocks"); ok && v != nil && v != false {
				if n, ok := toNum(v); ok {
					disk = string(n)
				}
			}
		}
		if text.IsInt(maxAcc) && text.Atoi(maxAcc) > 0 && text.Atoi(cur) > text.Atoi(maxAcc) {
			fmt.Fprintf(os.Stderr, "Error: Reseller '%s' has %s accounts, plan '%s' allows only %s. Remove accounts first.\n", username, cur, plan, maxAcc)
			return false
		}
		diskInt := disk
		if i := strings.LastIndex(diskInt, "."); i >= 0 {
			diskInt = diskInt[:i]
		}
		if text.IsInt(maxDisk) && text.Atoi(maxDisk) > 0 && text.Atoi(diskInt) > text.Atoi(maxDisk) {
			fmt.Fprintf(os.Stderr, "Error: Reseller '%s' uses %s disk blocks, plan '%s' allows only %s. Free up disk space first.\n", username, disk, plan, maxDisk)
			return false
		}
	}

	// only touch fields whose flag was actually passed, so e.g. --logo_url alone doesn't wipe the rest
	fail := func() {
		os.Remove(file + ".tmp")
		fmt.Printf("Error: failed to update reseller '%s' (invalid value?).\n", username)
		cli.Exit(1)
	}
	o, err := reseller.Load(file)
	if err != nil {
		fail()
	}
	setJSON := func(k, raw string) {
		v, err := ojson.Parse(raw)
		if err != nil {
			if k == "max_accounts" || k == "max_disk_blocks" {
				fmt.Fprint(os.Stderr, "jq: invalid JSON text passed to --argjson\nUse jq --help for help with command-line options,\nor see the jq manpage, or online docs  at https://jqlang.org\n")
			}
			fail()
		}
		o.Set(k, v)
	}
	if maxAcc != "" {
		setJSON("max_accounts", maxAcc)
	}
	if maxDisk != "" {
		setJSON("max_disk_blocks", maxDisk)
	}
	if allowed != "" {
		setJSON("allowed_plans", allowed)
	}
	if own != "" {
		setJSON("own_plans", own)
	}
	if plan != "" {
		o.Set("reseller_plan", plan)
	}
	if logoSet {
		o.Set("logo_url", logo)
	}
	os.WriteFile(file+".tmp", []byte(ojson.Pretty(o)+"\n"), 0o644)
	os.Rename(file+".tmp", file)
	fmt.Printf("Reseller %s updated successfully.\n", username)
	if logoSet {
		// pushes RESELLER_LOGO_URL into each owned user's .env since their containers can't reach the resellers dir directly
		db.Require()
		ctxs := db.Cmd("SELECT server FROM users WHERE owner='"+db.Escape(username)+"';", "-N", "-B").NoErr().Str()
		for _, c := range strings.Split(ctxs, "\n") {
			if c == "" {
				continue
			}
			env := "/home/" + c + "/.env"
			if !isFile(env) {
				continue
			}
			if !reseller.EnvSetLogo(env, logo) {
				fmt.Fprintln(os.Stderr, "Warning: could not set the logo URL in "+env)
			}
		}
	}
	return true
}

func rename(oldName, newName string) {
	eo, en := esc(oldName), esc(newName)
	exists := text.Atoi(sq("SELECT COUNT(*) FROM user WHERE username='" + eo + "';"))
	role := sq("SELECT role FROM user WHERE username='" + eo + "';")
	taken := text.Atoi(sq("SELECT COUNT(*) FROM user WHERE username='" + en + "';"))
	if exists <= 0 {
		fmt.Println(red + "Error" + reset + ": User '" + oldName + "' not found.")
		return
	}
	if taken > 0 {
		fmt.Println(red + "Error" + reset + ": Username '" + newName + "' already taken.")
		return
	}
	sq("UPDATE user SET username='" + en + "' WHERE username='" + eo + "';")
	fmt.Printf("User '%s' renamed to '%s'.\n", oldName, newName)
	if re, err := regexp.Compile(`\b` + oldName + `\b`); err == nil {
		if b, err := os.ReadFile(loginLog); err == nil {
			writeKeep(loginLog, re.ReplaceAllLiteralString(string(b), newName))
		}
	}
	act := "/var/log/openpanel/admin/activity/"
	if isFile(act + oldName + ".log") {
		os.Rename(act+oldName+".log", act+newName+".log")
	}
	if role == "reseller" {
		os.Rename("/etc/openpanel/features/"+oldName, "/etc/openpanel/features/"+newName)
		os.Rename(reseller.Dir+"/"+oldName+".json", reseller.Dir+"/"+newName+".json")
		db.Require()
		db.Cmd("UPDATE users SET owner='" + db.Escape(newName) + "' WHERE owner='" + db.Escape(oldName) + "';").Quiet().Run()
		sentinel("reseller_rename", "Reseller renamed", "Reseller account '"+oldName+"' has been renamed to '"+newName+"'.")
	} else {
		sentinel("admin_rename", "Administrator renamed", "Administrator account '"+oldName+"' has been renamed to '"+newName+"'.")
	}
}

func writeKeep(path, content string) {
	conf.SedFile(path, func([]string) []string { return strings.Split(strings.TrimSuffix(content, "\n"), "\n") })
}

func updatePassword(username, password string) {
	e := esc(username)
	exists := text.Atoi(sq("SELECT COUNT(*) FROM user WHERE username='" + e + "';"))
	pwHash := hash(password)
	if exists <= 0 {
		fmt.Println(red + "Error" + reset + ": User '" + username + "' not found.")
		return
	}
	sq("UPDATE user SET password_hash='" + esc(pwHash) + "' WHERE username='" + e + "';")
	sentinel("admin_password", "Administrator password changed", "Administrator account '"+username+"' has password changed.")
	bar := strings.Repeat("=", 63)
	fmt.Printf("Password for user '%s' changed.\n\n%s\n", username, bar)
	status()
	fmt.Printf("\n- username: %s\n- password: %s\n\n%s\n", username, password, bar)
}

func manageUser(action, username string) {
	e := esc(username)
	if !strings.Contains(sq("SELECT 1 FROM user WHERE username='"+e+"';"), "1") {
		fmt.Println(red + "Error" + reset + ": User '" + username + "' does not exist.")
		return
	}
	active, label, sa, title := "1", "unsuspended", "admin_unsuspend", "Administrator unsuspended"
	if action == "suspend" {
		if strings.Contains(sq("SELECT 1 FROM user WHERE username='"+e+"' AND role='admin';"), "1") {
			fmt.Println(red + "Error" + reset + ": Cannot suspend user '" + username + "' with 'admin' role.")
			return
		}
		active, label, sa, title = "0", "suspended", "admin_suspend", "Administrator suspended"
	}
	sq("UPDATE user SET is_active='" + active + "' WHERE username='" + e + "';")
	fmt.Printf("User '%s' %s successfully.\n", username, label)
	sentinel(sa, title, "Administrator account '"+username+"' has been "+label+".")
	if !strings.Contains(sq("SELECT 1 FROM user WHERE username='"+e+"' AND role='reseller';"), "1") {
		return
	}
	if action == "suspend" {
		suspendOwned(username)
	} else {
		unsuspendOwned(username)
	}
}

func setList(file, key string, vals []string, dropEmpty bool) {
	o, err := reseller.Load(file)
	if err != nil {
		return
	}
	arr := []any{}
	for _, v := range vals {
		arr = append(arr, v)
	}
	if dropEmpty && len(arr) == 0 {
		o.Delete(key)
	} else {
		o.Set(key, arr)
	}
	os.WriteFile(file+".tmp", []byte(ojson.Pretty(o)+"\n"), 0o644)
	os.Rename(file+".tmp", file)
}

func listOf(file, key string) []string {
	o, err := reseller.Load(file)
	if err != nil {
		return nil
	}
	v, _ := o.Get(key)
	arr, _ := v.([]any)
	var out []string
	for _, it := range arr {
		out = append(out, ojson.Text(it))
	}
	return out
}

// suspends the reseller's active accounts and remembers them, so unsuspend doesn't wake ones the reseller suspended themselves
func suspendOwned(username string) {
	db.Require()
	users, err := db.Cmd(`SELECT username FROM users WHERE owner='`+db.Escape(username)+`' AND username NOT LIKE 'SUSPENDED\_%';`, "-N", "-B").Out()
	if err != nil {
		return
	}
	var done []string
	for _, u := range strings.Split(users, "\n") {
		if u == "" {
			continue
		}
		if sh.Opencli("user-suspend", u, "-y").Quiet().OK() {
			done = append(done, u)
		} else {
			fmt.Printf("Warning: could not suspend account '%s'.\n", u)
		}
	}
	file := reseller.LimitsFile(username)
	merged := append(listOf(file, "suspended_with_reseller"), done...)
	sort.Strings(merged)
	var uniq []string
	for i, s := range merged {
		if i == 0 || s != merged[i-1] {
			uniq = append(uniq, s)
		}
	}
	setList(file, "suspended_with_reseller", uniq, false)
	fmt.Printf("Suspended %d account(s) owned by '%s'.\n", len(done), username)
}

func unsuspendOwned(username string) {
	file := reseller.LimitsFile(username)
	var failed []string
	for _, u := range listOf(file, "suspended_with_reseller") {
		if u == "" {
			continue
		}
		if !sh.Opencli("user-unsuspend", u).Quiet().OK() {
			failed = append(failed, u)
		}
	}
	// failed ones stay listed so the next unsuspend retries them
	setList(file, "suspended_with_reseller", failed, true)
	if len(failed) > 0 {
		fmt.Println("Warning: could not unsuspend these accounts: " + strings.Join(failed, " "))
	}
}

var suspendedRe = regexp.MustCompile(`^SUSPENDED_[0-9]{14}_`)

// owned accounts would keep running with an owner that no longer exists, so they move to admin or get deleted with --with-users
func handleOwned(username, withUsers string) bool {
	db.Require()
	owner := db.Escape(username)
	users, err := db.Cmd("SELECT username FROM users WHERE owner='"+owner+"';", "-N", "-B").Out()
	if err != nil {
		fmt.Println(red + "Error" + reset + ": Could not list the accounts owned by '" + username + "'.")
		return false
	}
	if users == "" {
		return true
	}
	if withUsers != "--with-users" {
		if db.Cmd("UPDATE users SET owner=NULL WHERE owner='"+owner+"';").Run() != nil {
			fmt.Println(red + "Error" + reset + ": Could not move the accounts owned by '" + username + "' to admin.")
			return false
		}
		fmt.Println("Accounts owned by '" + username + "' now belong to admin: " + strings.ReplaceAll(users+"\n", "\n", " "))
		return true
	}
	var failed []string
	for _, u := range strings.Split(users, "\n") {
		if u == "" {
			continue
		}
		// user-delete takes the original name, also for suspended accounts
		u = suspendedRe.ReplaceAllString(u, "")
		if !sh.Opencli("user-delete", u, "-y").Quiet().OK() {
			failed = append(failed, u)
		}
	}
	if len(failed) > 0 {
		fmt.Println(red + "Error" + reset + ": Could not delete these accounts owned by '" + username + "': " + strings.Join(failed, " ") + ". The reseller was not deleted.")
		return false
	}
	fmt.Println("Deleted all accounts owned by '" + username + "'.")
	return true
}

func deleteUser(username, withUsers string) bool {
	e := esc(username)
	exists := text.Atoi(sq("SELECT COUNT(*) FROM user WHERE username='" + e + "';"))
	isAdmin := text.Atoi(sq("SELECT COUNT(*) FROM user WHERE username='" + e + "' AND role='admin';"))
	if exists <= 0 {
		fmt.Println(red + "Error" + reset + ": User '" + username + "' does not exist.")
		return false
	}
	if isAdmin > 0 {
		fmt.Println(red + "Error" + reset + ": Cannot delete user '" + username + "' with 'admin' role.")
		return false
	}
	if !handleOwned(username, withUsers) {
		return false
	}
	os.Remove(reseller.Dir + "/" + username + ".json")
	// todo: also delete plans assigned only to this user
	os.RemoveAll("/etc/openpanel/features/" + username)
	sq("DELETE FROM user WHERE username='" + e + "';")
	os.Remove("/var/log/openpanel/admin/activity/" + username + ".log")
	fmt.Printf("User '%s' deleted successfully.\n", username)
	sentinel("admin_delete", "Administrator deleted", "Administrator account '"+username+"' has been deleted.")
	return true
}

// ##############################################################################
// Script Name: internal/commands/user/mcp.go
// Description: Create, list or revoke AI Assistant (MCP) tokens for an OpenPanel user and print client setup.
// Usage: opencli user-mcp <username> [--name=NAME] [--readonly|--no-deletes] [--features=a,b] [--expires=DAYS] [--client=NAME] [--json]
// Author: Stefan Pejcic
// Created: 23.11.2026
// Last Modified: 23.11.2026
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
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"opencli/internal/cli"
	"opencli/internal/conf"
	"opencli/internal/db"
)

func init() {
	cli.Register(&cli.Command{
		Name:  "user-mcp",
		Desc:  "Create, list or revoke AI Assistant (MCP) tokens for an OpenPanel user and print client setup.",
		Usage: "opencli user-mcp <username> [--name=NAME] [--readonly|--no-deletes] [--features=a,b] [--expires=DAYS] [--client=NAME] [--json]",
		Run:   mcp,
	})
}

const mcpEnterpriseLink = "https://my.openpanel.com/index.php?rp=/store/openpanel/enterprise-license"

var mcpClients = []string{"claude-code", "claude-desktop", "codex", "cursor", "vscode", "gemini"}

func mcpUsage() {
	fmt.Print(`
Usage: opencli user-mcp <USERNAME> [options]
Docs:  https://openpanel.com/docs/opencli/user/#ai-assistant-mcp-tokens

Create a token and print how to connect an MCP client with it:
  --name=NAME        Token name (default: "opencli YYYY-MM-DD").
  --readonly         The token can only look things up.
  --no-deletes       The token can create and change things, but not delete, restore or change passwords.
  --features=a,b     Limit the token to these features, e.g. dns,domains. The user must have them.
  --expires=DAYS     Expire the token after DAYS days (default: never).
  --client=NAME      Only print setup for one client: claude-code, claude-desktop, codex, cursor, vscode, gemini.
  --json             Print the token and MCP URL as JSON instead of setup instructions.

Manage tokens:
  --list             List the user's tokens (never the token values).
  --revoke=ID        Revoke one token by its ID from --list.

Examples:
  opencli user-mcp stefan
  opencli user-mcp stefan --no-deletes --features=dns,domains --expires=30 --client=claude-code
  opencli user-mcp stefan --list
  opencli user-mcp stefan --revoke=4

`)
}

func mcpDie(msg string) {
	cli.Failerr("Error: %s", msg)
}

type mcpOpts struct {
	username string
	name     string
	mode     string
	features string
	expires  int
	client   string
	json     bool
	action   string
	revokeID string
}

var (
	mcpUserRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	mcpFeaturesRe = regexp.MustCompile(`^[a-z0-9_]+(,[a-z0-9_]+)*$`)
	mcpDigitsRe   = regexp.MustCompile(`^[0-9]+$`)
)

// mcpParse reads and checks the flags, help means print the usage first
func mcpParse(args []string, today string) (o mcpOpts, help bool, errMsg string) {
	o = mcpOpts{mode: "full", action: "create"}
	expires := "0"
	for _, a := range args[1:] {
		switch {
		case strings.HasPrefix(a, "--name="):
			o.name = strings.TrimPrefix(a, "--name=")
		case a == "--readonly":
			o.mode = "readonly"
		case a == "--no-deletes":
			o.mode = "modify"
		case strings.HasPrefix(a, "--features="):
			o.features = strings.TrimPrefix(a, "--features=")
		case strings.HasPrefix(a, "--expires="):
			expires = strings.TrimPrefix(a, "--expires=")
		case strings.HasPrefix(a, "--client="):
			o.client = strings.TrimPrefix(a, "--client=")
		case a == "--json":
			o.json = true
		case a == "--list":
			o.action = "list"
		case strings.HasPrefix(a, "--revoke="):
			o.action = "revoke"
			o.revokeID = strings.TrimPrefix(a, "--revoke=")
		case a == "-h" || a == "--help":
			return o, true, ""
		default:
			return o, true, "unknown option '" + a + "'"
		}
	}
	o.username = args[0]
	if !mcpUserRe.MatchString(o.username) {
		return o, false, "invalid username '" + o.username + "'"
	}
	if !mcpDigitsRe.MatchString(expires) {
		return o, false, "--expires takes a number of days"
	}
	// a huge number would overflow the int or the DATE_ADD
	n, err := strconv.Atoi(expires)
	if err != nil || n > 36500 {
		return o, false, "--expires can be at most 36500 days"
	}
	o.expires = n
	if o.client != "" && !mcpIsClient(o.client) {
		return o, false, "--client must be one of: " + strings.Join(mcpClients, " ")
	}
	if o.name == "" {
		o.name = "opencli " + today
	}
	if utf8.RuneCountInString(o.name) > 100 {
		return o, false, "--name is longer than 100 characters"
	}
	// tabs and newlines would break --list
	if strings.IndexFunc(o.name, unicode.IsControl) >= 0 {
		return o, false, "--name can't have tabs, newlines or other control characters"
	}
	if o.features != "" {
		o.features = mcpCleanFeatures(o.features)
		// a typo would make a token that can do nothing, the panel ignores unknown names
		if !mcpFeaturesRe.MatchString(o.features) {
			return o, false, "--features takes feature names like dns,domains"
		}
	}
	return o, false, ""
}

func mcpIsClient(c string) bool {
	for _, k := range mcpClients {
		if k == c {
			return true
		}
	}
	return false
}

// mcpCleanFeatures drops blanks and duplicates and sorts, like tr | sed | sort -u | paste
func mcpCleanFeatures(s string) string {
	seen := map[string]bool{}
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// mcpNewToken is the panel's own shape: op_mcp_ + 32 random bytes in unpadded base64url, stored as a sha256
func mcpNewToken() (raw, prefix, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	raw = "op_mcp_" + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, raw[:13], hex.EncodeToString(sum[:]), nil
}

// mcpUnbatch undoes the escaping mariadb -B does on values
func mcpUnbatch(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\\`, `\`, `\t`, "\t", `\n`, "\n", `\0`, "\x00").Replace(s)
}

func mcpScopeColumnsPresent() bool {
	return db.Value("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mcp_tokens' AND COLUMN_NAME IN ('mode', 'allowed_features');") == "2"
}

// the panel adds these on first use too, both use plain ADD COLUMN so it works on MySQL and MariaDB
func mcpEnsureScopeColumns() error {
	for _, col := range []string{"mode", "allowed_features"} {
		has, err := db.Query("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'mcp_tokens' AND COLUMN_NAME = '" + col + "';")
		if err != nil {
			return err
		}
		if has == "1" {
			continue
		}
		def := "VARCHAR(10) NULL DEFAULT NULL"
		if col == "allowed_features" {
			def = "TEXT NULL DEFAULT NULL"
		}
		if err := db.Exec("ALTER TABLE mcp_tokens ADD COLUMN " + col + " " + def + ";"); err != nil {
			return err
		}
	}
	return nil
}

func mcpLogActivity(username, msg string) {
	dir := "/etc/openpanel/openpanel/core/users/" + username
	if !conf.IsDir(dir) {
		return
	}
	f, err := os.OpenFile(dir+"/activity.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  127.0.0.1 User %s %s\n", time.Now().Format("2006-01-02 15:04:05"), username, msg)
}

// features.txt next to the account wins, else the plan's feature set, same as the panel
func mcpUserFeatures(context, userID string) []string {
	file := "/home/" + context + "/features.txt"
	if !conf.IsFile(file) {
		set := db.Value("SELECT plans.feature_set FROM users JOIN plans ON users.plan_id = plans.id WHERE users.id = " + userID + ";")
		file = "/etc/openpanel/openpanel/features/" + set + ".txt"
		if set == "" || strings.Contains(set, "/") || !conf.IsFile(file) {
			file = "/etc/openpanel/openpanel/features/default.txt"
		}
	}
	var out []string
	for _, l := range conf.Lines(file) {
		if l = strings.Join(strings.Fields(l), ""); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// mcpBaselineFeatures every account has no matter the plan, same list as the panel's baselineFeatures
var mcpBaselineFeatures = []string{"dashboard", "helpers", "websites", "databases_size_info", "screenshots", "favicons", "logout", "errors", "search", "app"}

// mcpFeatureOnlyModules drop out of the account when they're off on Settings > Modules, same as the panel
var mcpFeatureOnlyModules = map[string]bool{
	"ssl": true, "redirects": true, "php_options": true, "php_ini": true, "php_extensions": true, "phpmyadmin": true,
	"edit_vhost": true, "docroot": true, "domain_logs": true, "capitalize_domains": true, "domain_suspend": true, "temporary_links": true,
}

// mcpSensitiveFeatures can take over the account or hand out a database login, a token only gets them when they're named
var mcpSensitiveFeatures = map[string]bool{"account": true, "passkeys": true, "phpmyadmin": true, "twofa": true}

// mcpLiveFeatures is what the panel itself allows the account: the feature file, the baseline, minus switched-off modules
// mcpModuleEnabled is swapped in tests, which can't depend on the openpanel.config of the machine running them
var mcpModuleEnabled = conf.ModuleEnabled

func mcpLiveFeatures(fileFeatures []string) []string {
	var out []string
	for _, f := range append(append([]string{}, fileFeatures...), mcpBaselineFeatures...) {
		if mcpFeatureOnlyModules[f] && !mcpModuleEnabled(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// mcpDefaultFeatures is the list a token gets without --features: everything the account has except api, mcp and the sensitive ones
func mcpDefaultFeatures(live []string) string {
	var out []string
	for _, f := range live {
		if f != "api" && f != "mcp" && !mcpSensitiveFeatures[f] {
			out = append(out, f)
		}
	}
	return mcpCleanFeatures(strings.Join(out, ","))
}

// mcpMissingFeatures is api, mcp and every --features name the user doesn't have
func mcpMissingFeatures(have []string, features string) []string {
	set := map[string]bool{}
	for _, f := range have {
		set[f] = true
	}
	needed := []string{"api", "mcp"}
	if features != "" {
		needed = append(needed, strings.Split(features, ",")...)
	}
	var missing []string
	for _, f := range needed {
		if !set[f] {
			missing = append(missing, "'"+f+"'")
		}
	}
	return missing
}

// same lookup as user-login, the MCP endpoint lives on the user panel
func mcpPanelURL() string {
	return strings.TrimSuffix(loginOpenPanelURL(), "/")
}

func mcpClientSetup(client, url, token string) string {
	switch client {
	case "claude-code":
		return `Claude Code (run in a terminal):

  claude mcp add --transport http openpanel ` + url + ` \
    --header "Authorization: Bearer ` + token + `"

`
	case "claude-desktop":
		return `Claude Desktop (claude_desktop_config.json, needs Node.js for mcp-remote):

  "openpanel": {
    "command": "npx",
    "args": ["-y", "mcp-remote", "` + url + `", "--header", "Authorization:${OPENPANEL_AUTH}"],
    "env": { "OPENPANEL_AUTH": "Bearer ` + token + `" }
  }

`
	case "codex":
		return `Codex (~/.codex/config.toml):

  [mcp_servers.openpanel]
  url = "` + url + `"
  http_headers = { Authorization = "Bearer ` + token + `" }

`
	case "cursor":
		return `Cursor (~/.cursor/mcp.json):

  {
    "mcpServers": {
      "openpanel": {
        "url": "` + url + `",
        "headers": { "Authorization": "Bearer ` + token + `" }
      }
    }
  }

`
	case "vscode":
		return `VS Code / GitHub Copilot (.vscode/mcp.json):

  {
    "servers": {
      "openpanel": {
        "type": "http",
        "url": "` + url + `",
        "headers": { "Authorization": "Bearer ` + token + `" }
      }
    }
  }

`
	case "gemini":
		return `Gemini CLI (run in a terminal):

  gemini mcp add --transport http --header "Authorization: Bearer ` + token + `" openpanel ` + url + `

`
	}
	return ""
}

// mcpNote is the ", read-only, features: dns domains, expires in 30d" bit for the message and the activity log
func mcpNote(o mcpOpts, allDefault bool) string {
	note := ""
	switch o.mode {
	case "readonly":
		note = ", read-only"
	case "modify":
		note = ", no deletes"
	}
	switch {
	case allDefault:
		note += ", all features except account, 2FA, passkeys and phpMyAdmin"
	case o.features != "":
		note += ", features: " + strings.ReplaceAll(o.features, ",", " ")
	}
	if o.expires > 0 {
		note += fmt.Sprintf(", expires in %dd", o.expires)
	}
	return note
}

type mcpTokenRow struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Mode       string `json:"mode"`
	Features   string `json:"features"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
	LastUsedAt string `json:"last_used_at"`
}

func mcpParseRows(rows [][]string) []mcpTokenRow {
	out := []mcpTokenRow{}
	for _, r := range rows {
		if len(r) < 8 {
			continue
		}
		id, err := strconv.Atoi(r[0])
		if err != nil {
			continue
		}
		out = append(out, mcpTokenRow{ID: id, Name: mcpUnbatch(r[1]), Prefix: r[2], Mode: r[3], Features: r[4], CreatedAt: r[5], ExpiresAt: r[6], LastUsedAt: r[7]})
	}
	return out
}

func mcpListTable(username string, rows []mcpTokenRow) string {
	if len(rows) == 0 {
		return "User " + username + " has no MCP tokens.\n"
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tTOKEN\tACCESS\tFEATURES\tCREATED\tEXPIRES\tLAST USED")
	for _, r := range rows {
		access := r.Mode
		switch access {
		case "readonly":
			access = "read-only"
		case "modify":
			access = "no deletes"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Name, r.Prefix, access, r.Features, r.CreatedAt, r.ExpiresAt, r.LastUsedAt)
	}
	w.Flush()
	return b.String()
}

func mcp(args []string) int {
	first := cli.Arg(args, 0)
	if first == "" || strings.HasPrefix(first, "-") {
		mcpUsage()
		return 1
	}
	o, help, errMsg := mcpParse(args, time.Now().Format("2006-01-02"))
	if help {
		mcpUsage()
		if errMsg == "" {
			return 0
		}
	}
	if errMsg != "" {
		mcpDie(errMsg)
	}

	if v, _ := conf.Raw(conf.Panel, "key"); v == "" {
		fmt.Println("Error: OpenPanel Community edition does not support API access, which MCP tokens need. Please consider purchasing the Enterprise version.")
		fmt.Println(mcpEnterpriseLink)
		return 1
	}

	db.Require()
	userRow, err := db.Query("SELECT id, server FROM users WHERE username='" + db.Escape(o.username) + "' LIMIT 1;")
	if err != nil {
		mcpDie("could not look up user '" + o.username + "' in the database")
	}
	fields := strings.Split(userRow, "\t")
	userID := fields[0]
	if !mcpDigitsRe.MatchString(userID) {
		mcpDie("user '" + o.username + "' does not exist")
	}
	context := o.username
	if len(fields) > 1 && fields[1] != "" && fields[1] != "NULL" {
		context = fields[1]
	}

	switch o.action {
	case "list":
		q := "SELECT id, name, token_prefix, IF(read_only, 'readonly', 'full'), '', created_at, IFNULL(expires_at, ''), IFNULL(last_used_at, '') FROM mcp_tokens WHERE user_id = " + userID + " ORDER BY id;"
		if mcpScopeColumnsPresent() {
			q = "SELECT id, name, token_prefix, IFNULL(mode, IF(read_only, 'readonly', 'full')), IFNULL(allowed_features, ''), created_at, IFNULL(expires_at, ''), IFNULL(last_used_at, '') FROM mcp_tokens WHERE user_id = " + userID + " ORDER BY id;"
		}
		out, err := db.Query(q)
		if err != nil {
			mcpDie("could not read the tokens")
		}
		rows := mcpParseRows(db.Split(out))
		if o.json {
			b, _ := json.Marshal(rows)
			fmt.Println(string(b))
			return 0
		}
		fmt.Print(mcpListTable(o.username, rows))
		return 0

	case "revoke":
		if !mcpDigitsRe.MatchString(o.revokeID) {
			mcpDie("--revoke takes a token ID from --list")
		}
		name := mcpUnbatch(db.Value("SELECT name FROM mcp_tokens WHERE id = " + o.revokeID + " AND user_id = " + userID + ";"))
		if name == "" {
			mcpDie("user " + o.username + " has no token with ID " + o.revokeID)
		}
		if err := db.Exec("DELETE FROM mcp_tokens WHERE id = " + o.revokeID + " AND user_id = " + userID + ";"); err != nil {
			mcpDie("could not revoke the token")
		}
		mcpLogActivity(o.username, "revoked MCP token #"+o.revokeID+" via opencli")
		fmt.Printf("Revoked MCP token #%s (%s) for user %s. Clients using it stop working right away.\n", o.revokeID, name, o.username)
		return 0
	}

	// same rule as the token page and the api, only features the user actually has
	live := mcpLiveFeatures(mcpUserFeatures(context, userID))
	if missing := mcpMissingFeatures(live, o.features); len(missing) > 0 {
		word, them := "feature", "it"
		if len(missing) > 1 {
			word, them = "features", "them"
		}
		mcpDie(o.username + " doesn't have the " + strings.Join(missing, ", ") + " " + word + ", add " + them + " to their plan or feature set first")
	}

	// the token keeps this list, features the user gets later don't reach it on their own
	allDefault := o.features == ""
	if allDefault {
		o.features = mcpDefaultFeatures(live)
		if o.features == "" {
			mcpDie(o.username + " has no features a token could use besides api and mcp")
		}
	}
	if err := mcpEnsureScopeColumns(); err != nil {
		mcpDie("could not add the mode and allowed_features columns to mcp_tokens, which every token needs now")
	}

	raw, prefix, hash, err := mcpNewToken()
	if err != nil {
		mcpDie("could not make a random token")
	}
	readOnly := "0"
	if o.mode == "readonly" {
		readOnly = "1"
	}
	expiresSQL := "NULL"
	if o.expires > 0 {
		expiresSQL = fmt.Sprintf("DATE_ADD(NOW(), INTERVAL %d DAY)", o.expires)
	}
	insert := "INSERT INTO mcp_tokens (user_id, name, token_prefix, token_hash, read_only, expires_at, mode, allowed_features) VALUES (" + userID + ", '" + db.Escape(o.name) + "', '" + prefix + "', '" + hash + "', " + readOnly + ", " + expiresSQL + ", '" + o.mode + "', '" + o.features + "');"
	if err := db.Exec(insert); err != nil {
		mcpDie("could not save the token")
	}

	note := mcpNote(o, allDefault)
	mcpLogActivity(o.username, "created a new MCP token ("+o.name+note+") via opencli")

	url := mcpPanelURL() + "/mcp"
	if o.json {
		b, _ := json.Marshal(struct {
			Token    string `json:"token"`
			URL      string `json:"url"`
			Name     string `json:"name"`
			Mode     string `json:"mode"`
			Features string `json:"features"`
			Expires  int    `json:"expires_in_days"`
		}{raw, url, o.name, o.mode, o.features, o.expires})
		fmt.Println(string(b))
		return 0
	}

	fmt.Printf("Created MCP token \"%s\" for user %s%s.\n", o.name, o.username, note)
	fmt.Printf("It is shown only once. Revoke it with: opencli user-mcp %s --revoke=<ID from --list>\n\n", o.username)
	fmt.Println("MCP URL: " + url)
	fmt.Println("Token:   " + raw)
	fmt.Println()
	clients := mcpClients
	if o.client != "" {
		clients = []string{o.client}
	}
	for _, c := range clients {
		fmt.Print(mcpClientSetup(c, url, raw))
	}

	if v, _ := conf.Raw(conf.Panel, "enabled_modules"); conf.StripQuotes(v) != "" {
		for _, needed := range []string{"api", "mcp"} {
			if !conf.ModuleEnabled(needed) {
				fmt.Printf("Warning: the '%s' module is disabled server-wide, enable it in OpenAdmin > Settings > Modules.\n", needed)
			}
		}
	}
	return 0
}

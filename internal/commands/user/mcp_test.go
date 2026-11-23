// ##############################################################################
// Script Name: internal/commands/user/mcp_test.go
// Description: Tests for opencli user-mcp.
// Usage: go test ./internal/commands/user/
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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"opencli/internal/cli"
)

func TestMCPParseDefaults(t *testing.T) {
	o, help, err := mcpParse([]string{"stefan"}, "2026-11-23")
	if help || err != "" {
		t.Fatalf("help=%v err=%q", help, err)
	}
	if o.username != "stefan" || o.mode != "full" || o.action != "create" || o.name != "opencli 2026-11-23" || o.expires != 0 || o.json {
		t.Fatalf("got %+v", o)
	}
}

func TestMCPParseFlags(t *testing.T) {
	o, _, err := mcpParse([]string{"stefan", "--name=ci bot", "--no-deletes", "--features= domains, dns,,dns ", "--expires=30", "--client=cursor", "--json"}, "x")
	if err != "" {
		t.Fatal(err)
	}
	if o.name != "ci bot" || o.mode != "modify" || o.features != "dns,domains" || o.expires != 30 || o.client != "cursor" || !o.json {
		t.Fatalf("got %+v", o)
	}
	if o, _, _ := mcpParse([]string{"stefan", "--readonly"}, "x"); o.mode != "readonly" {
		t.Fatalf("readonly: %+v", o)
	}
	if o, _, _ := mcpParse([]string{"stefan", "--list", "--json"}, "x"); o.action != "list" || !o.json {
		t.Fatalf("list: %+v", o)
	}
	if o, _, _ := mcpParse([]string{"stefan", "--revoke=4"}, "x"); o.action != "revoke" || o.revokeID != "4" {
		t.Fatalf("revoke: %+v", o)
	}
}

func TestMCPParseRejects(t *testing.T) {
	cases := map[string][]string{
		"invalid username":    {"ste fan"},
		"number of days":      {"stefan", "--expires=-1"},
		"at most 36500":       {"stefan", "--expires=99999999999999999999"},
		"--client must be":    {"stefan", "--client=notepad"},
		"longer than 100":     {"stefan", "--name=" + strings.Repeat("a", 101)},
		"control characters":  {"stefan", "--name=a\tb"},
		"feature names like":  {"stefan", "--features=DNS"},
		"unknown option '-x'": {"stefan", "-x"},
	}
	for want, args := range cases {
		_, _, err := mcpParse(args, "x")
		if !strings.Contains(err, want) {
			t.Errorf("%v: got %q, want %q", args, err, want)
		}
	}
	if _, help, err := mcpParse([]string{"stefan", "--help"}, "x"); !help || err != "" {
		t.Errorf("--help: help=%v err=%q", help, err)
	}
	// 100 characters is still fine, counted as characters not bytes
	if _, _, err := mcpParse([]string{"stefan", "--name=" + strings.Repeat("é", 100)}, "x"); err != "" {
		t.Errorf("100 chars: %q", err)
	}
}

func TestMCPNewToken(t *testing.T) {
	raw, prefix, hash, err := mcpNewToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "op_mcp_") || prefix != raw[:13] {
		t.Fatalf("raw %q prefix %q", raw, prefix)
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, "op_mcp_"))
	if err != nil || len(b) != 32 {
		t.Fatalf("body isn't 32 bytes of base64url: %v %d", err, len(b))
	}
	sum := sha256.Sum256([]byte(raw))
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatal("hash isn't the sha256 of the token")
	}
	if raw2, _, _, _ := mcpNewToken(); raw2 == raw {
		t.Fatal("two tokens are the same")
	}
}

func TestMCPRowsAndList(t *testing.T) {
	out := "4\tci \\\\ bot\top_mcp_AbCdEf\tmodify\tdns,domains\t2026-11-23 10:00:00\t\t\n7\tread\top_mcp_123456\treadonly\t\t2026-11-23 11:00:00\t2026-12-23 11:00:00\t2026-11-23 12:00:00"
	rows := mcpParseRows([][]string{{"x"}, strings.Split(strings.Split(out, "\n")[0], "\t"), strings.Split(strings.Split(out, "\n")[1], "\t")})
	if len(rows) != 2 || rows[0].ID != 4 || rows[0].Name != `ci \ bot` || rows[1].ExpiresAt != "2026-12-23 11:00:00" {
		t.Fatalf("rows %+v", rows)
	}
	b, _ := json.Marshal(rows)
	if !strings.HasPrefix(string(b), `[{"id":4,"name":"ci \\ bot","prefix":"op_mcp_AbCdEf","mode":"modify","features":"dns,domains",`) {
		t.Fatalf("json %s", b)
	}
	if b, _ := json.Marshal(mcpParseRows(nil)); string(b) != "[]" {
		t.Fatalf("empty list should be [], got %s", b)
	}

	table := mcpListTable("stefan", rows)
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID  NAME") || !strings.Contains(lines[1], "no deletes") || !strings.Contains(lines[2], "read-only") {
		t.Fatalf("table:\n%s", table)
	}
	if got := mcpListTable("stefan", nil); got != "User stefan has no MCP tokens.\n" {
		t.Fatalf("empty: %q", got)
	}
}

func TestMCPUnbatch(t *testing.T) {
	if got := mcpUnbatch(`a\\b\tc`); got != "a\\b\tc" {
		t.Fatalf("got %q", got)
	}
}

func TestMCPClientSetup(t *testing.T) {
	url, tok := "https://server.example.com:2083/mcp", "op_mcp_TOKEN"
	for _, c := range mcpClients {
		s := mcpClientSetup(c, url, tok)
		if !strings.Contains(s, url) || !strings.Contains(s, "Bearer "+tok) {
			t.Errorf("%s setup is missing the url or token:\n%s", c, s)
		}
	}
	// Claude Desktop reads the header from env so the token isn't split on the space
	if s := mcpClientSetup("claude-desktop", url, tok); !strings.Contains(s, `"Authorization:${OPENPANEL_AUTH}"`) {
		t.Errorf("claude-desktop:\n%s", s)
	}
	// the snippets that are JSON have to parse once the label line is gone
	for _, c := range []string{"cursor", "vscode"} {
		s := mcpClientSetup(c, url, tok)
		var v map[string]any
		if err := json.Unmarshal([]byte(s[strings.Index(s, "{"):]), &v); err != nil {
			t.Errorf("%s isn't valid JSON: %v", c, err)
		}
	}
	if mcpClientSetup("notepad", url, tok) != "" {
		t.Error("unknown client printed something")
	}
}

func TestMCPNote(t *testing.T) {
	if got := mcpNote(mcpOpts{mode: "modify", features: "dns,domains", expires: 30}); got != ", no deletes, features: dns domains, expires in 30d" {
		t.Fatalf("got %q", got)
	}
	if got := mcpNote(mcpOpts{mode: "full"}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestMCPRegistered(t *testing.T) {
	c := cli.Lookup("user-mcp")
	if c == nil || !strings.HasPrefix(c.Usage, "opencli user-mcp <username>") {
		t.Fatal("user-mcp isn't registered")
	}
	// no username prints usage and exits 1 before touching the config or database
	if code := cli.Invoke(c, nil); code != 1 {
		t.Fatalf("no args: exit %d", code)
	}
	if code := cli.Invoke(c, []string{"--list"}); code != 1 {
		t.Fatalf("flag first: exit %d", code)
	}
}

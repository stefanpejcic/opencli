package domains

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRlTrustedEntry(t *testing.T) {
	cases := map[string]string{
		"198.51.100.7":    "198.51.100.7",
		"198.51.100.7/24": "198.51.100.0/24",
		"2001:db8::1":     "2001:db8::1",
		"2001:db8::/32":   "2001:db8::/32",
	}
	for in, want := range cases {
		if got, ok := rlTrustedEntry(in); !ok || got != want {
			t.Fatalf("rlTrustedEntry(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "example.com", "1.2.3", "tcp|in|d=22|s=1.2.3.4", "1.2.3.4/40"} {
		if _, ok := rlTrustedEntry(bad); ok {
			t.Fatalf("rlTrustedEntry(%q) accepted", bad)
		}
	}
}

// plain entries count, comments, Include lines and port-limited advanced filters don't
func TestRlCSFEntries(t *testing.T) {
	dir := t.TempDir()
	allow := filepath.Join(dir, "csf.allow")
	ignore := filepath.Join(dir, "csf.ignore")
	os.WriteFile(allow, []byte("# comment\n198.51.100.7 # office\ntcp|in|d=22|s=203.0.113.9\nInclude /etc/csf/cpanel.allow\n10.0.0.0/8\n"), 0o600)
	os.WriteFile(ignore, []byte("198.51.100.7\n2001:db8::1\n"), 0o600)
	old := rlCSFLists
	rlCSFLists = []string{allow, ignore, filepath.Join(dir, "missing")}
	t.Cleanup(func() { rlCSFLists = old })

	got := strings.Join(rlCSFEntries(), " ")
	if got != "10.0.0.0/8 198.51.100.7 2001:db8::1" {
		t.Fatalf("rlCSFEntries = %q", got)
	}
}

func TestRlTrustedContent(t *testing.T) {
	c := rlTrustedContent([]string{"192.0.2.1"}, []string{"198.51.100.7", "192.0.2.1"})
	if !strings.Contains(c, "\nclient_ip 127.0.0.1/32 ::1/128 192.0.2.1 198.51.100.7\n") {
		t.Fatalf("client_ip line wrong:\n%s", c)
	}
	if !strings.Contains(c, "\n# manual: 192.0.2.1\n") {
		t.Fatalf("manual line wrong:\n%s", c)
	}
	// with nothing configured loopback keeps the not {} matcher from being empty
	if !strings.HasSuffix(rlTrustedContent(nil, nil), "\nclient_ip 127.0.0.1/32 ::1/128\n") {
		t.Fatal("empty list lost loopback")
	}
}

func TestRlReadManualRoundTrip(t *testing.T) {
	dir := t.TempDir()
	old := rlTrustedConf
	rlTrustedConf = filepath.Join(dir, "ratelimit_trusted.conf")
	t.Cleanup(func() { rlTrustedConf = old })
	os.WriteFile(rlTrustedConf, []byte(rlTrustedContent([]string{"192.0.2.1", "198.51.100.0/24"}, nil)), 0o644)
	if got := strings.Join(rlReadManual(), " "); got != "192.0.2.1 198.51.100.0/24" {
		t.Fatalf("rlReadManual = %q", got)
	}
}

package email

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opencli/internal/conf"
)

func TestKeyFileKeepsTheFirstKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.key")
	a, err := keyFile(p)
	if err != nil || len(a) != 64 {
		t.Fatalf("%q %v", a, err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if b, _ := keyFile(p); b != a {
		t.Error("key changed on second read")
	}
}

func TestGolubEnvKeepsSessionKey(t *testing.T) {
	dir := t.TempDir()
	golubEnvFile = filepath.Join(dir, "golub.env")
	golubSSOKey = filepath.Join(dir, "sso.key")
	dovecotKey = filepath.Join(dir, "dovecot.key")
	os.WriteFile(dovecotKey, []byte("masterpass\n"), 0o600)

	if err := golubWriteEnv(); err != nil {
		t.Fatal(err)
	}
	first, _ := conf.Raw(golubEnvFile, "GOLUB_SECRET_KEY")
	if err := golubWriteEnv(); err != nil {
		t.Fatal(err)
	}
	second, _ := conf.Raw(golubEnvFile, "GOLUB_SECRET_KEY")
	if first == "" || first != second {
		t.Fatalf("session key changed: %q -> %q", first, second)
	}
	env := conf.Read(golubEnvFile)
	sso, _ := os.ReadFile(golubSSOKey)
	for _, want := range []string{"GOLUB_MASTER_USER=openpanel", "GOLUB_MASTER_PASSWORD=masterpass", "GOLUB_SSO_SECRET=" + string(sso)} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in\n%s", want, env)
		}
	}
	if st, _ := os.Stat(golubEnvFile); st.Mode().Perm() != 0o600 {
		t.Errorf("golub.env mode %v", st.Mode())
	}
}

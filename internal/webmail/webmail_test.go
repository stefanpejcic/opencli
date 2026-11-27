package webmail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestService(t *testing.T) {
	EnvFile = filepath.Join(t.TempDir(), "mailserver.env")
	if Service() != "roundcube" || Container() != "openadmin_roundcube" {
		t.Fatal("no env file means roundcube")
	}
	for in, want := range map[string]string{"WEBMAIL_CLIENT=golub\n": "golub", "WEBMAIL_CLIENT=roundcube\n": "roundcube", "WEBMAIL_CLIENT=\n": "roundcube", "WEBMAIL_CLIENT=sogo\n": "roundcube"} {
		os.WriteFile(EnvFile, []byte("SSL_TYPE=\n"+in), 0o644)
		if got := Service(); got != want {
			t.Errorf("%q: %s", in, got)
		}
	}
}

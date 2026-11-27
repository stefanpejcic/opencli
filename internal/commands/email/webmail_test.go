package email

import "testing"

func TestWmHostRe(t *testing.T) {
	re := wmHostRe("mail.example.com")
	for in, want := range map[string]bool{
		"mail.example.com {":         true,
		"http://mail.example.com {":  true,
		"webmail.example.com {":      false,
		"mail.example.com.au {":      false,
		"mailxexample.com {":         false,
		"reverse_proxy localhost:80": false,
	} {
		if got := re.MatchString(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

func TestWmStripBlock(t *testing.T) {
	cf := "example.com {\n}\n# START WEBMAIL DOMAIN #\nwebmail.example.com {\n    reverse_proxy localhost:8080\n}\n# END WEBMAIL DOMAIN #\nother.example.com {\n}\n"
	out := wmStripBlock(cf)
	if wmHostRe("webmail.example.com").MatchString(out) {
		t.Errorf("webmail block not stripped:\n%s", out)
	}
	if !wmHostRe("other.example.com").MatchString(out) || !wmHostRe("example.com").MatchString(out) {
		t.Errorf("stripped too much:\n%s", out)
	}
}

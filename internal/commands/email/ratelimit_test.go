package email

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRlBuildRuleSharesAccountCounter(t *testing.T) {
	r := rlBuildRule("stefan", "100", "example.com")
	for _, want := range []string{
		"id=limit_stefan_example_com ; sender=~.+@example.com$ ; protocol_state==RCPT\n",
		"action=set(op_account=stefan)\n",
		"id=limit_stefan_rate ; sender=~.+@example.com$ ; protocol_state==RCPT\n",
		"action=rate(op_account/100/3600/450 4.7.1 sorry, OpenPanel account reached limit of 100 emails per hour)\n\n",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("rule misses %q:\n%s", want, r)
		}
	}
}

func TestRlRemove(t *testing.T) {
	f := filepath.Join(t.TempDir(), "postfwd.cf")
	// restore appends pairs without a blank line between them
	content := rlBuildRule("ana", "100", "a.com") + rlBuildRule("bob", "300", "b.com") +
		"id=limit_ana_c_com ; sender=~.+@c.com ; protocol_state==RCPT\n                action=rate(ana_ratelimit/100/3600/450 x)\n" +
		"id=limit_cid_d_com ; sender=~.+@d.com ; protocol_state==RCPT\n                action=rate(cid_ratelimit/100/3600/450 x)\n"
	os.WriteFile(f, []byte(content), 0o644)

	got := rlRemove(f, "id=limit_ana_")
	if strings.Contains(got, "ana") || !strings.Contains(got, "id=limit_bob_rate ") || !strings.Contains(got, "id=limit_cid_d_com ") {
		t.Errorf("remove user:\n%s", got)
	}

	got = rlRemoveDomain(f, "b.com")
	if strings.Contains(got, "@b.com") || !strings.Contains(got, "id=limit_ana_rate ") || !strings.Contains(got, "id=limit_cid_d_com ") {
		t.Errorf("remove domain:\n%s", got)
	}
	got = rlRemoveDomain(f, "c.com")
	if strings.Contains(got, "@c.com") || !strings.Contains(got, "@a.com$") || !strings.Contains(got, "id=limit_cid_d_com ") {
		t.Errorf("remove id:\n%s", got)
	}
}

func TestSrvStripPolicy(t *testing.T) {
	in := srvPostfwdPolicy + ", permit_sasl_authenticated, check_policy_service unix:private/policyd-spf"
	if got := srvStripPolicy(in); got != "permit_sasl_authenticated, check_policy_service unix:private/policyd-spf" {
		t.Errorf("got %q", got)
	}
}

func TestSrvPostfwdComposeEnv(t *testing.T) {
	old := "services:\n  postfwd:\n    image: postfwd/postfwd:stable\n    environment:\n      - EXTRA=--keep_rates\n    container_name: postfwd\n  mailserver:\n    environment:\n      - EXTRA=keep\n"
	got, changed := srvPostfwdComposeEnv(old)
	want := "services:\n  postfwd:\n    image: postfwd/postfwd:stable\n" + srvPostfwdEnv + "    container_name: postfwd\n  mailserver:\n    environment:\n      - EXTRA=keep\n"
	if !changed || got != want {
		t.Fatalf("got:\n%s", got)
	}
	if again, changed := srvPostfwdComposeEnv(got); changed || again != got {
		t.Fatalf("second run changed it:\n%s", again)
	}
}

package sh

import (
	"slices"
	"testing"
)

func TestLowPrioArgs(t *testing.T) {
	got := LowPrioArgs("tar", "-czf", "x.tgz")
	if !slices.Equal(got[len(got)-3:], []string{"tar", "-czf", "x.tgz"}) {
		t.Fatalf("command not kept at the end: %v", got)
	}
	if Has("nice") && !slices.Contains(got, "-n19") {
		t.Fatalf("nice missing: %v", got)
	}
	if Has("ionice") && !slices.Contains(got, "-c3") {
		t.Fatalf("ionice missing: %v", got)
	}
}

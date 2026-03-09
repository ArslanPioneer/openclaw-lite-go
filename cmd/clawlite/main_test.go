package main

import "testing"

func TestResolveHealthListenAddrUsesAllInterfaces(t *testing.T) {
	got := resolveHealthListenAddr(18080)
	if got != ":18080" {
		t.Fatalf("expected :18080, got %q", got)
	}
}

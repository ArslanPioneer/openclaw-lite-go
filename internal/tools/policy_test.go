package tools

import (
	"errors"
	"testing"
)

func TestIsMutatingCall(t *testing.T) {
	if !IsMutatingCall(Call{Name: "skill_install", Skill: "daily-ai-news"}) {
		t.Fatal("expected skill_install to be treated as mutating")
	}
	if IsMutatingCall(Call{Name: "web_search", Query: "openclaw"}) {
		t.Fatal("expected web_search to be non-mutating")
	}
}

func TestPendingMutationFailureMatchesFingerprint(t *testing.T) {
	call := Call{Name: "skill_install", Skill: "daily-ai-news"}
	failure := NewPendingMutationFailure(call, errors.New("permission denied"))
	if failure.Tool == "" || failure.Fingerprint == "" {
		t.Fatalf("expected populated pending mutation failure, got %+v", failure)
	}

	if !failure.Matches(call) {
		t.Fatal("expected fingerprint to match same action")
	}
	if failure.Matches(Call{Name: "skill_install", Skill: "another-skill"}) {
		t.Fatal("expected fingerprint mismatch for different action")
	}
}

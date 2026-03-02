package tools

import (
	"fmt"
	"strings"
)

type PendingMutationFailure struct {
	Tool        string
	Fingerprint string
	Error       string
}

func IsMutatingCall(call Call) bool {
	switch strings.ToLower(strings.TrimSpace(call.Name)) {
	case "skill_install":
		return true
	default:
		return false
	}
}

func NewPendingMutationFailure(call Call, err error) PendingMutationFailure {
	msg := "unknown error"
	if err != nil {
		msg = strings.TrimSpace(err.Error())
		if msg == "" {
			msg = "unknown error"
		}
	}
	return PendingMutationFailure{
		Tool:        strings.ToLower(strings.TrimSpace(call.Name)),
		Fingerprint: callFingerprint(call),
		Error:       msg,
	}
}

func (p PendingMutationFailure) Matches(call Call) bool {
	return strings.TrimSpace(p.Fingerprint) != "" && p.Fingerprint == callFingerprint(call)
}

func callFingerprint(call Call) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(call.Name)),
		strings.TrimSpace(call.Query),
		strings.TrimSpace(call.URL),
		strings.TrimSpace(call.Text),
		strings.TrimSpace(call.Skill),
		strings.TrimSpace(call.Script),
		strings.TrimSpace(call.Input),
		fmt.Sprintf("all=%t", call.All),
		fmt.Sprintf("max=%d", call.MaxBytes),
	}
	return strings.Join(parts, "|")
}

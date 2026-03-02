package runtime

import "strings"

// These values are injected at build time via -ldflags.
var (
	AppVersion = "dev"
	AppCommit  = "unknown"
)

func BuildVersionString() string {
	version := strings.TrimSpace(AppVersion)
	if version == "" {
		version = "dev"
	}
	commit := strings.TrimSpace(AppCommit)
	if commit == "" || commit == "unknown" {
		return version
	}
	return version + " (" + commit + ")"
}

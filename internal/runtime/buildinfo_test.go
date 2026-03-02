package runtime

import "testing"

func TestBuildVersionString(t *testing.T) {
	originalVersion := AppVersion
	originalCommit := AppCommit
	t.Cleanup(func() {
		AppVersion = originalVersion
		AppCommit = originalCommit
	})

	AppVersion = "v1.2.3"
	AppCommit = "abc1234"
	if got := BuildVersionString(); got != "v1.2.3 (abc1234)" {
		t.Fatalf("BuildVersionString() = %q", got)
	}

	AppVersion = "v1.2.3"
	AppCommit = ""
	if got := BuildVersionString(); got != "v1.2.3" {
		t.Fatalf("BuildVersionString() without commit = %q", got)
	}
}

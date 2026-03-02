package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallListRead(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source")
	install := filepath.Join(tmp, "install")

	skillDir := filepath.Join(source, "weather")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir source skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: weather\n---\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	manager := NewManager(source, install)
	path, err := manager.Install("weather")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}

	list, err := manager.ListInstalled()
	if err != nil {
		t.Fatalf("ListInstalled() error = %v", err)
	}
	if len(list) != 1 || list[0] != "weather" {
		t.Fatalf("unexpected installed list: %#v", list)
	}

	content, err := manager.ReadSkill("weather", 1024)
	if err != nil {
		t.Fatalf("ReadSkill() error = %v", err)
	}
	if content == "" {
		t.Fatal("expected non-empty skill content")
	}
}

func TestInstallRejectsBadName(t *testing.T) {
	manager := NewManager("src", "dst")
	if _, err := manager.Install("../hack"); err == nil {
		t.Fatal("expected invalid skill name error")
	}
}

func TestListAvailable(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "source")
	install := filepath.Join(tmp, "install")

	aDir := filepath.Join(source, "alpha")
	if err := os.MkdirAll(aDir, 0o755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(aDir, "SKILL.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}

	bDir := filepath.Join(source, "beta")
	if err := os.MkdirAll(bDir, 0o755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bDir, "SKILL.md"), []byte("beta"), 0o644); err != nil {
		t.Fatalf("write beta: %v", err)
	}

	ignored := filepath.Join(source, "ignore-me")
	if err := os.MkdirAll(ignored, 0o755); err != nil {
		t.Fatalf("mkdir ignore: %v", err)
	}

	manager := NewManager(source, install)
	names, err := manager.ListAvailable()
	if err != nil {
		t.Fatalf("ListAvailable() error = %v", err)
	}

	joined := strings.Join(names, ",")
	if joined != "alpha,beta" {
		t.Fatalf("unexpected available list: %v", names)
	}
}

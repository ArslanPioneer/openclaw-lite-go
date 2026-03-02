package skills

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Manager struct {
	sourceDir  string
	installDir string
}

func NewManager(sourceDir string, installDir string) *Manager {
	return &Manager{
		sourceDir:  strings.TrimSpace(sourceDir),
		installDir: strings.TrimSpace(installDir),
	}
}

func (m *Manager) Install(skill string) (string, error) {
	name, err := sanitizeSkillName(skill)
	if err != nil {
		return "", err
	}

	source := filepath.Join(m.sourceDir, name)
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("skill %q not found in source dir %q", name, m.sourceDir)
		}
		return "", fmt.Errorf("check source skill: %w", err)
	}

	dest := filepath.Join(m.installDir, name)
	if err := os.RemoveAll(dest); err != nil {
		return "", fmt.Errorf("remove existing installed skill: %w", err)
	}
	if err := copyDir(source, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func (m *Manager) ListInstalled() ([]string, error) {
	return listWithSkillManifest(m.installDir)
}

func (m *Manager) ListAvailable() ([]string, error) {
	return listWithSkillManifest(m.sourceDir)
}

func listWithSkillManifest(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, entry.Name(), "SKILL.md")); err == nil {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (m *Manager) ReadSkill(skill string, maxBytes int) (string, error) {
	name, err := sanitizeSkillName(skill)
	if err != nil {
		return "", err
	}

	path := filepath.Join(m.installDir, name, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read skill file: %w", err)
	}
	if maxBytes <= 0 || len(data) <= maxBytes {
		return string(data), nil
	}
	return string(data[:maxBytes]) + "\n...[truncated]", nil
}

func (m *Manager) RunScript(ctx context.Context, skill string, script string, input string) (string, error) {
	name, err := sanitizeSkillName(skill)
	if err != nil {
		return "", err
	}
	cleanScript := filepath.ToSlash(strings.TrimSpace(script))
	if cleanScript == "" || !strings.HasPrefix(cleanScript, "scripts/") || strings.Contains(cleanScript, "..") {
		return "", fmt.Errorf("script path must be inside scripts/: %q", script)
	}

	skillDir := filepath.Join(m.installDir, name)
	scriptPath := filepath.Join(skillDir, filepath.FromSlash(cleanScript))
	if _, err := os.Stat(scriptPath); err != nil {
		return "", fmt.Errorf("skill script not found: %w", err)
	}

	var cmd *exec.Cmd
	switch strings.ToLower(filepath.Ext(scriptPath)) {
	case ".py":
		cmd = exec.CommandContext(ctx, "python", scriptPath)
	case ".sh":
		cmd = exec.CommandContext(ctx, "sh", scriptPath)
	case ".ps1":
		cmd = exec.CommandContext(ctx, "powershell", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	default:
		return "", fmt.Errorf("unsupported script type: %s", filepath.Ext(scriptPath))
	}
	cmd.Dir = skillDir
	cmd.Env = append(os.Environ(), "SKILL_INPUT="+input)

	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return text, fmt.Errorf("run skill script failed: %w", err)
	}
	return text, nil
}

func sanitizeSkillName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid skill name: %q", raw)
	}
	return name, nil
}

func copyDir(src string, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		info, err := srcFile.Stat()
		if err != nil {
			return err
		}
		dstFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return err
		}
		return nil
	})
}

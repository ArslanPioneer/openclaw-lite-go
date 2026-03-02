package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMissingRequiredFields(t *testing.T) {
	cfg := Config{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	message := err.Error()
	for _, key := range []string{"telegram.bot_token", "agent.base_url", "agent.api_key", "agent.model"} {
		if !strings.Contains(message, key) {
			t.Fatalf("expected validation error to include %q, got %q", key, message)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")

	original := Config{
		Telegram: TelegramConfig{
			BotToken: "tg-token",
		},
		Agent: AgentConfig{
			Provider:     "openai",
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "sk-test",
			Model:        "gpt-4o-mini",
			SystemPrompt: "you are concise",
		},
		Runtime: RuntimeConfig{
			Workers:           8,
			QueueSize:         128,
			PollTimeoutSecond: 20,
			RequestTimeoutSec: 45,
			DataDir:           "data",
			HistoryTurns:      10,
			AgentRetryCount:   3,
			HealthPort:        18080,
			RestartBackoffMs:  1000,
			RestartMaxMs:      30000,
		},
	}

	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.Telegram.BotToken != original.Telegram.BotToken {
		t.Fatalf("bot token mismatch: got %q want %q", loaded.Telegram.BotToken, original.Telegram.BotToken)
	}
	if loaded.Agent.Model != original.Agent.Model {
		t.Fatalf("model mismatch: got %q want %q", loaded.Agent.Model, original.Agent.Model)
	}
	if loaded.Agent.Provider != original.Agent.Provider {
		t.Fatalf("provider mismatch: got %q want %q", loaded.Agent.Provider, original.Agent.Provider)
	}
	if loaded.Runtime.Workers != original.Runtime.Workers {
		t.Fatalf("workers mismatch: got %d want %d", loaded.Runtime.Workers, original.Runtime.Workers)
	}
	if loaded.Runtime.HealthPort != original.Runtime.HealthPort {
		t.Fatalf("health port mismatch: got %d want %d", loaded.Runtime.HealthPort, original.Runtime.HealthPort)
	}
	if loaded.Runtime.HistoryTurns != original.Runtime.HistoryTurns {
		t.Fatalf("history turns mismatch: got %d want %d", loaded.Runtime.HistoryTurns, original.Runtime.HistoryTurns)
	}
	if loaded.Runtime.AgentRetryCount != original.Runtime.AgentRetryCount {
		t.Fatalf("agent retry mismatch: got %d want %d", loaded.Runtime.AgentRetryCount, original.Runtime.AgentRetryCount)
	}
}

func TestApplyDefaultsProviderBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantURL  string
	}{
		{name: "openai", provider: "openai", wantURL: "https://api.openai.com/v1"},
		{name: "minimax", provider: "minimax", wantURL: "https://api.minimaxi.com/v1"},
		{name: "glm", provider: "glm", wantURL: "https://open.bigmodel.cn/api/paas/v4"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				Telegram: TelegramConfig{BotToken: "tg-token"},
				Agent: AgentConfig{
					Provider: tc.provider,
					APIKey:   "sk-test",
					Model:    "model-x",
				},
			}
			cfg.ApplyDefaults()
			if cfg.Agent.BaseURL != tc.wantURL {
				t.Fatalf("BaseURL mismatch: got %q want %q", cfg.Agent.BaseURL, tc.wantURL)
			}
		})
	}
}

func TestValidateRejectsUnknownProvider(t *testing.T) {
	cfg := Config{
		Telegram: TelegramConfig{BotToken: "tg-token"},
		Agent: AgentConfig{
			Provider: "unknown",
			BaseURL:  "https://example.com/v1",
			APIKey:   "sk-test",
			Model:    "model-x",
		},
	}
	cfg.ApplyDefaults()

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for unknown provider")
	}
}

func TestApplyDefaultsSetsSkillsDirectories(t *testing.T) {
	cfg := Config{
		Telegram: TelegramConfig{BotToken: "tg-token"},
		Agent: AgentConfig{
			Provider: "openai",
			APIKey:   "sk-test",
			Model:    "model-x",
		},
		Runtime: RuntimeConfig{
			DataDir: "runtime-data",
		},
	}

	cfg.ApplyDefaults()

	if cfg.Runtime.SkillsSourceDir != "openclaw-skills" {
		t.Fatalf("unexpected skills_source_dir: %q", cfg.Runtime.SkillsSourceDir)
	}
	if cfg.Runtime.SkillsInstallDir != filepath.Join("runtime-data", "skills") {
		t.Fatalf("unexpected skills_install_dir: %q", cfg.Runtime.SkillsInstallDir)
	}
}

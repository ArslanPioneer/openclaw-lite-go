package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultWorkers           = 4
	defaultQueueSize         = 64
	defaultPollTimeoutSecond = 25
	defaultRequestTimeoutSec = 60
	defaultHealthPort        = 18080
	defaultRestartBackoffMs  = 1000
	defaultRestartMaxMs      = 30000
	defaultDataDir           = "data"
	defaultHistoryTurns      = 8
	defaultAgentRetryCount   = 2
	defaultCodexProxyTimeout = 120
)

const (
	ProviderOpenAI  = "openai"
	ProviderMiniMax = "minimax"
	ProviderGLM     = "glm"
	ProviderCustom  = "custom"
)

type Config struct {
	Telegram TelegramConfig `json:"telegram"`
	Agent    AgentConfig    `json:"agent"`
	Runtime  RuntimeConfig  `json:"runtime"`
}

type TelegramConfig struct {
	BotToken string `json:"bot_token"`
}

type AgentConfig struct {
	Provider     string `json:"provider,omitempty"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt,omitempty"`
}

type RuntimeConfig struct {
	Workers           int    `json:"workers"`
	QueueSize         int    `json:"queue_size"`
	PollTimeoutSecond int    `json:"poll_timeout_second"`
	RequestTimeoutSec int    `json:"request_timeout_sec"`
	HealthPort        int    `json:"health_port"`
	RestartBackoffMs  int    `json:"restart_backoff_ms"`
	RestartMaxMs      int    `json:"restart_max_ms"`
	DataDir           string `json:"data_dir,omitempty"`
	HistoryTurns      int    `json:"history_turns,omitempty"`
	AgentRetryCount   int    `json:"agent_retry_count,omitempty"`
	SkillsSourceDir   string `json:"skills_source_dir,omitempty"`
	SkillsInstallDir  string `json:"skills_install_dir,omitempty"`
	CodexProxyURL     string `json:"codex_proxy_url,omitempty"`
	CodexProxyToken   string `json:"codex_proxy_token,omitempty"`
	CodexProxyTimeout int    `json:"codex_proxy_timeout_sec,omitempty"`
}

const defaultTaskSystemPrompt = `You are ClawLite, a pragmatic task assistant.
Be concise, factual, and action-oriented.
When external information is required, you may request exactly one tool call per response with this strict format:
TOOL_CALL {"name":"web_search","query":"...","recency_days":7,"max_results":5}
or
TOOL_CALL {"name":"http_get","url":"https://..."}
or
TOOL_CALL {"name":"skill_install","skill":"weather"}
or
TOOL_CALL {"name":"skill_list"}
or
TOOL_CALL {"name":"skill_read","skill":"weather","max_bytes":4000}
or
TOOL_CALL {"name":"skill_run","skill":"weather","script":"scripts/run.py","input":"..."}
or
TOOL_CALL {"name":"docker_ps"}
or
TOOL_CALL {"name":"docker_ps","all":true}
or
TOOL_CALL {"name":"stock_price","query":"NVDA"}
You may perform multiple tool-call rounds when needed.
For web_search, prefer recency_days for freshness control and include citeable sources from tool output in your final answer.
When the user asks stock price/quote, prefer stock_price.
If stock_price is unavailable, use web_search for the latest quote context.
When the user asks about current host/container deployment status, prefer docker_ps instead of giving shell commands.
After tool output is provided, return the final answer directly without another tool call.`

func (c *Config) ApplyDefaults() {
	c.Agent.Provider = normalizeProvider(c.Agent.Provider)
	if strings.TrimSpace(c.Agent.BaseURL) == "" {
		c.Agent.BaseURL = defaultBaseURLForProvider(c.Agent.Provider)
	}
	if strings.TrimSpace(c.Agent.SystemPrompt) == "" {
		c.Agent.SystemPrompt = defaultTaskSystemPrompt
	}

	if c.Runtime.Workers <= 0 {
		c.Runtime.Workers = defaultWorkers
	}
	if c.Runtime.QueueSize <= 0 {
		c.Runtime.QueueSize = defaultQueueSize
	}
	if c.Runtime.PollTimeoutSecond <= 0 {
		c.Runtime.PollTimeoutSecond = defaultPollTimeoutSecond
	}
	if c.Runtime.RequestTimeoutSec <= 0 {
		c.Runtime.RequestTimeoutSec = defaultRequestTimeoutSec
	}
	if c.Runtime.HealthPort <= 0 {
		c.Runtime.HealthPort = defaultHealthPort
	}
	if c.Runtime.RestartBackoffMs <= 0 {
		c.Runtime.RestartBackoffMs = defaultRestartBackoffMs
	}
	if c.Runtime.RestartMaxMs <= 0 {
		c.Runtime.RestartMaxMs = defaultRestartMaxMs
	}
	if strings.TrimSpace(c.Runtime.DataDir) == "" {
		c.Runtime.DataDir = defaultDataDir
	}
	if c.Runtime.HistoryTurns <= 0 {
		c.Runtime.HistoryTurns = defaultHistoryTurns
	}
	if c.Runtime.AgentRetryCount <= 0 {
		c.Runtime.AgentRetryCount = defaultAgentRetryCount
	}
	if strings.TrimSpace(c.Runtime.SkillsInstallDir) == "" {
		c.Runtime.SkillsInstallDir = filepath.Join(c.Runtime.DataDir, "skills")
	}
	if strings.TrimSpace(c.Runtime.SkillsSourceDir) == "" {
		c.Runtime.SkillsSourceDir = "openclaw-skills"
	}
	if c.Runtime.CodexProxyTimeout <= 0 {
		c.Runtime.CodexProxyTimeout = defaultCodexProxyTimeout
	}
}

func (c Config) Validate() error {
	missing := make([]string, 0, 4)
	provider := normalizeProvider(c.Agent.Provider)
	if !isSupportedProvider(provider) {
		return fmt.Errorf("invalid config: unsupported agent.provider %q", c.Agent.Provider)
	}
	if strings.TrimSpace(c.Telegram.BotToken) == "" {
		missing = append(missing, "telegram.bot_token")
	}
	if strings.TrimSpace(c.Agent.BaseURL) == "" {
		missing = append(missing, "agent.base_url")
	}
	if strings.TrimSpace(c.Agent.APIKey) == "" {
		missing = append(missing, "agent.api_key")
	}
	if strings.TrimSpace(c.Agent.Model) == "" {
		missing = append(missing, "agent.model")
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid config: missing required fields: %s", strings.Join(missing, ", "))
	}
	if c.Runtime.HealthPort < 0 || c.Runtime.HealthPort > 65535 {
		return fmt.Errorf("invalid config: runtime.health_port must be 0-65535")
	}
	if c.Runtime.RestartBackoffMs <= 0 {
		return fmt.Errorf("invalid config: runtime.restart_backoff_ms must be > 0")
	}
	if c.Runtime.RestartMaxMs < c.Runtime.RestartBackoffMs {
		return fmt.Errorf("invalid config: runtime.restart_max_ms must be >= runtime.restart_backoff_ms")
	}
	if strings.TrimSpace(c.Runtime.DataDir) == "" {
		return fmt.Errorf("invalid config: runtime.data_dir is required")
	}
	if c.Runtime.HistoryTurns <= 0 {
		return fmt.Errorf("invalid config: runtime.history_turns must be > 0")
	}
	if c.Runtime.AgentRetryCount <= 0 {
		return fmt.Errorf("invalid config: runtime.agent_retry_count must be > 0")
	}
	if strings.TrimSpace(c.Runtime.SkillsSourceDir) == "" {
		return fmt.Errorf("invalid config: runtime.skills_source_dir is required")
	}
	if strings.TrimSpace(c.Runtime.SkillsInstallDir) == "" {
		return fmt.Errorf("invalid config: runtime.skills_install_dir is required")
	}
	if c.Runtime.CodexProxyTimeout <= 0 {
		return fmt.Errorf("invalid config: runtime.codex_proxy_timeout_sec must be > 0")
	}
	if proxyURL := strings.TrimSpace(c.Runtime.CodexProxyURL); proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
			return fmt.Errorf("invalid config: runtime.codex_proxy_url must be a valid http/https url")
		}
	}
	return nil
}

func normalizeProvider(raw string) string {
	provider := strings.ToLower(strings.TrimSpace(raw))
	if provider == "" {
		return ProviderOpenAI
	}
	return provider
}

func isSupportedProvider(provider string) bool {
	switch provider {
	case ProviderOpenAI, ProviderMiniMax, ProviderGLM, ProviderCustom:
		return true
	default:
		return false
	}
}

func defaultBaseURLForProvider(provider string) string {
	switch provider {
	case ProviderMiniMax:
		return "https://api.minimaxi.com/v1"
	case ProviderGLM:
		return "https://open.bigmodel.cn/api/paas/v4"
	case ProviderCustom:
		return ""
	default:
		return "https://api.openai.com/v1"
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	cfg.ApplyDefaults()

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

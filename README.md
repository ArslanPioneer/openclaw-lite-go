# OpenClaw Lite (Go)

OpenClaw Lite is a minimal, Telegram-only assistant inspired by `openclaw/openclaw`, rewritten with a lean Go runtime for lower overhead and faster cold start.

## Scope

- Keep: Telegram message loop, agent call, per-chat model switch
- Remove: multi-channel plugins, heavy onboarding layers, UI/control-plane complexity
- Add: one-command setup for token + agent
- Add: intelligent minimum pack (persistent memory + minimal tools + retry)
- Add: OpenClaw skills bridge (`skill_install`, `skill_list`, `skill_read`, `skill_run`)

## Project Layout

- `cmd/clawlite/main.go`: CLI entry (`setup`, `run`)
- `cmd/codexproxy/main.go`: VPS-side Codex CLI HTTP proxy (`/chat`)
- `internal/config`: config load/save/validate
- `internal/telegram`: Telegram Bot API client (long polling + send)
- `internal/agent`: OpenAI-compatible chat completion adapter
- `internal/memory`: per-chat persistent memory with summary compaction
- `internal/runtime`: worker-pool runtime and command handling
- `internal/codexproxy`: Codex CLI proxy server and transcript state
- `internal/tools`: minimal built-in tool executor (`web_search`, `http_get`, `echo`, `skill_*`)
- `internal/skills`: install/read/run manager for skill directories (`SKILL.md` + `scripts/*`)
- `scripts/quickstart.ps1`: one-click setup script
- `scripts/sync-openclaw-skills.*`: sync skills from `../openclaw/skills` to `./openclaw-skills`
- `scripts/docker-deploy.*`: one-click Docker deploy
- `scripts/docker-update.*`: one-click Docker update

## Quick Start

1. Create Telegram bot token from `@BotFather`.
2. Prepare an OpenAI-compatible endpoint and API key if you want the legacy fallback path available.
3. Run one-click setup:

```powershell
pwsh ./scripts/quickstart.ps1 `
  -Provider "openai" `
  -TelegramToken "123456:ABCDEF" `
  -AgentKey "sk-..." `
  -AgentModel "gpt-4o-mini"
```

4. Start service:

```powershell
go run ./cmd/clawlite run --config ./config.json
```

You can also start from [`config.example.json`](./config.example.json).

## CLI

### Setup

```powershell
go run ./cmd/clawlite setup `
  --provider openai `
  --telegram-token "123456:ABCDEF" `
  --agent-key "sk-..." `
  --agent-model "gpt-4o-mini" `
  --skills-source-dir "openclaw-skills" `
  --skills-install-dir "data/skills" `
  --data-dir "data" `
  --history-turns 8 `
  --agent-retry-count 2 `
  --health-port 18080 `
  --restart-backoff-ms 1000 `
  --restart-max-ms 30000
```

Provider presets:

- `openai` -> `https://api.openai.com/v1`
- `minimax` -> `https://api.minimaxi.com/v1`
- `glm` -> `https://open.bigmodel.cn/api/paas/v4`
- `custom` -> requires explicit `--agent-url`

### Run

```powershell
go run ./cmd/clawlite run --config ./config.json
```

## Telegram Commands

- `/start`: startup hint
- `/agent <model>`: switch the fallback legacy agent model for current chat only
- `/codex [model|off]`: switch current chat to Codex model (`gpt-5-codex` by default)
- `/agentmode <legacy|codex>`: switch the current chat between legacy agent flow and codex-first proxy flow
- `/codexcli [on|off]`: compatibility alias for `/agentmode codex|legacy`
- `/goal`: show the active goal for the current chat
- `/goals`: list recent goals for the current chat
- `/goalstop`: stop the active goal for the current chat
- `/confirm`: approve the pending host-critical codex action for this chat
- `/price <ticker>`: direct stock quote (example: `/price NVDA`)
- `/skills`: list installable skills from `runtime.skills_source_dir`
- `/skills installed`: list installed skills from `runtime.skills_install_dir`
- `/skills sync`: install all available skills into `runtime.skills_install_dir`
- `/skills install <skill_name>`: install one skill by name
- `/version`: show deployed build version and commit

Telegram replies are rendered from markdown-ish text into Telegram-safe `HTML` by default; if Telegram rejects the formatted payload, the bot retries once as plain text automatically.

## Intelligent Minimum Pack

- Persistent chat memory (file-based): per-chat history stored in `runtime.data_dir`.
- Summary memory: when history exceeds `runtime.history_turns`, older turns are compacted into summary memory.
- Retry on model failures: `runtime.agent_retry_count`.
- Task-oriented default system prompt with tool-call convention.
- Multi-step agent loop: supports iterative `plan -> tool -> reflect -> next tool/final answer` flow (bounded loop).
- OpenClaw skills bridge: install/list/read/run skills from configured runtime directories.
- Codex proxy research path: time-sensitive prompts can prefetch citeable web sources before Codex answers.
- Codex proxy audit log: each Codex execution records prompt/reply metadata in the proxy state dir.

Tool call format (model -> runtime):

```text
TOOL_CALL {"name":"web_search","query":"latest minimax model pricing","recency_days":7,"max_results":5}
```

Supported tools:
- `web_search`
- `http_get`
- `echo` (debug/test)
- `skill_install`
- `skill_list`
- `skill_read`
- `skill_run`
- `docker_ps` (reads host Docker containers via `/var/run/docker.sock`)
- `stock_price` (stock quote, Yahoo primary + Stooq fallback)

`web_search` options:
- `query` (required): search query text
- `recency_days` (optional): freshness control, mapped to provider time buckets
- `max_results` (optional): max number of cited sources returned (default 5, max 10)

`web_search` output includes citeable source entries with title, URL, and snippet.

### OpenClaw Skills As Tool Supplement

1. Prepare source skills directory (local default: `openclaw-skills`):

```powershell
.\scripts\sync-openclaw-skills.ps1
```

Linux/macOS:

```bash
chmod +x ./scripts/sync-openclaw-skills.sh
./scripts/sync-openclaw-skills.sh
```

2. Set runtime config:

```json
"runtime": {
  "skills_source_dir": "openclaw-skills",
  "skills_install_dir": "data/skills"
}
```

3. Ask model to call skill tools when needed. Tool call formats:

```text
TOOL_CALL {"name":"skill_install","skill":"news-aggregator-skill"}
TOOL_CALL {"name":"skill_list"}
TOOL_CALL {"name":"skill_read","skill":"news-aggregator-skill","max_bytes":4000}
TOOL_CALL {"name":"skill_run","skill":"news-aggregator-skill","script":"scripts/run.py","input":"topic=ai"}
TOOL_CALL {"name":"docker_ps"}
TOOL_CALL {"name":"docker_ps","all":true}
TOOL_CALL {"name":"stock_price","query":"NVDA"}
```

### Codex Proxy Mode (VPS Middleware)

If you run a Codex middleware service on VPS, configure:

```json
"runtime": {
  "codex_proxy_url": "http://127.0.0.1:8099/chat",
  "codex_proxy_token": "",
  "codex_proxy_timeout_sec": 120,
  "codex_first_default": true
}
```

If `clawlite` itself runs inside Docker but `codexproxy` runs on the VPS host via `systemd`,
use `http://host.docker.internal:8099/chat` instead of `127.0.0.1`, and keep the
`docker-compose.yml` host-gateway mapping enabled.

Then in Telegram:
- normal chat messages route to the Codex middleware by default
- `/agentmode legacy` to return one chat to the legacy agent path
- `/agentmode codex` to switch that chat back to codex-first mode
- `/codexcli on|off` remains available as a migration alias
- current/latest questions trigger an explicit research prefetch so Codex can answer with sources
- host-critical command text is risk-classified for audit/policy handling in full-access mode
- host-critical actions are paused and require explicit `/confirm` before execution continues
- if the Codex proxy is missing, the bot now asks for explicit `/agentmode legacy` instead of silently falling back

### Codex Proxy Deployment (Ubuntu + `codex login --device-auth`)

The bot already knows how to POST to a Codex proxy. The missing piece on VPS is `cmd/codexproxy`, which wraps the local Codex CLI and keeps a per-chat transcript under `.codexproxy/`.

1. Install Node.js LTS and Codex CLI on the VPS:

```bash
sudo apt update
sudo apt install -y curl ca-certificates build-essential
curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash -
sudo apt install -y nodejs
sudo npm i -g @openai/codex
codex login --device-auth
```

2. Build both binaries inside the deployed repo:

```bash
cd /opt/openclaw-lite-go
go build -o bin/clawlite ./cmd/clawlite
go build -o bin/codexproxy ./cmd/codexproxy
```

3. Start the Codex proxy locally on the VPS:

```bash
cd /opt/openclaw-lite-go
./bin/codexproxy \
  --listen 127.0.0.1:8099 \
  --workdir /opt/openclaw-lite-go \
  --state-dir /opt/openclaw-lite-go/.codexproxy \
  --codex-bin codex \
  --token "replace-with-random-secret"
```

If you want Telegram messages to let Codex act as a full VPS operator instead of a sandboxed repo agent, add:

```bash
  --danger-full-access
```

This makes `codexproxy` pass `--dangerously-bypass-approvals-and-sandbox` to `codex exec`. Only use it on a VPS you are willing to trust with unrestricted command execution.

4. Point `config.json` at the local proxy:

```json
"runtime": {
  "codex_proxy_url": "http://127.0.0.1:8099/chat",
  "codex_proxy_token": "replace-with-random-secret",
  "codex_proxy_timeout_sec": 600,
  "codex_first_default": true
}
```

5. Run the bot and toggle Telegram chat passthrough:

```bash
cd /opt/openclaw-lite-go
./bin/clawlite run --config ./config.json
```

Then in Telegram:
- send a normal message
- `/agentmode legacy` to return that chat to the OpenAI-compatible agent path
- `/agentmode codex` or `/codexcli on` to switch it back

Example `systemd` unit for `codexproxy`:

```ini
[Unit]
Description=OpenClaw Codex Proxy
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/openclaw-lite-go
ExecStart=/opt/openclaw-lite-go/bin/codexproxy --listen 127.0.0.1:8099 --workdir /opt/openclaw-lite-go --state-dir /opt/openclaw-lite-go/.codexproxy --codex-bin codex --token replace-with-random-secret --danger-full-access
Restart=always
RestartSec=3
User=root
Environment=HOME=/root

[Install]
WantedBy=multi-user.target
```

## Docker One-Click Deploy

`docker-compose.yml` runs the bot with:
- restart policy: `unless-stopped`
- persistent config mount: `./data -> /app/data`
- optional skills source mount: `./openclaw-skills -> /app/openclaw-skills` (read-only)
- optional Docker socket mount: `/var/run/docker.sock -> /var/run/docker.sock` (read-only)
- host gateway alias: `host.docker.internal -> host-gateway` so the container can reach host-side `codexproxy`
- health endpoint: `http://127.0.0.1:18080/healthz`

### Windows (PowerShell)

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\docker-deploy.ps1 `
  -Provider "openai" `
  -TelegramToken "123456:ABCDEF" `
  -AgentKey "sk-..." `
  -AgentModel "gpt-4o-mini"
```

Update:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\docker-update.ps1 -PullCode
```

### Linux/macOS (Shell)

```bash
chmod +x ./scripts/docker-deploy.sh ./scripts/docker-update.sh
TELEGRAM_TOKEN="123456:ABCDEF" AGENT_KEY="sk-..." ./scripts/docker-deploy.sh

# MiniMax example
PROVIDER=minimax TELEGRAM_TOKEN="123456:ABCDEF" AGENT_KEY="..." AGENT_MODEL="MiniMax-Text-01" ./scripts/docker-deploy.sh

# GLM example
PROVIDER=glm TELEGRAM_TOKEN="123456:ABCDEF" AGENT_KEY="..." AGENT_MODEL="glm-4.5" ./scripts/docker-deploy.sh
```

Update:

```bash
PULL_CODE=true ./scripts/docker-update.sh
```

After deploy, config is generated at `./data/config.json`.

## GitHub Auto Deploy

This repo includes a GitHub Actions workflow at `.github/workflows/deploy-vps.yml`.
It runs on every push to `main` (and supports manual trigger), executes `go test ./...`,
then deploys to VPS over SSH by pulling the latest `main` and running Docker Compose.

Required repository secrets:
- `VPS_HOST`
- `VPS_PORT`
- `VPS_USER`
- `VPS_PASSWORD`

Deployment behavior:
- If `/opt/openclaw-lite-go` is not a git repo, it backs up the old directory and clones from GitHub.
- Preserves `data/` and `.codexproxy/` from backup when recreating directory.
- Stops/disables legacy `openclaw-lite-go.service` to avoid host port `18080` conflicts.
- Migrates legacy `codex_proxy_url=http://127.0.0.1:8099/chat` to `http://host.docker.internal:8099/chat` for Docker-based bot deployments.
- Runs `docker compose up -d --build --remove-orphans --force-recreate`.

## Performance Notes

- Single binary design (no plugin runtime)
- Shared keep-alive HTTP client for both Telegram and agent calls
- Configurable worker pool and bounded queue
- Telegram offset checkpointing with in-memory update dedupe

## Resilience

- Worker panic recovery (`ProcessUpdate`) to avoid process crash from one bad update.
- Supervisor restart with exponential backoff for unexpected runtime exit/panic.
- Local health endpoint: `GET http://127.0.0.1:18080/healthz` (configurable via `runtime.health_port`).

## Release Gates

- Unit/integration tests: `go test ./...`
- Reliability eval gate: `go run ./scripts/evals/run.go -cases ./scripts/evals/cases.json`
- Codex-first acceptance gate: `go run ./scripts/evals/run.go -cases ./scripts/evals/codex_first_cases.json`
  - Expected pass ratio: `>= 90%`
  - Covers queued acknowledgement, background completion, host-critical `/confirm`, and confirm replay flows
- Benchmark smoke check: `go test ./internal/runtime -bench BenchmarkHandleUpdate -benchmem -run ^$`
  - Regression budget recommendation: `<= 15%` vs baseline

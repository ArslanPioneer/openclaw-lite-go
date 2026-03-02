#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")"/.. && pwd)"
HOST_DATA_DIR="$ROOT_DIR/data"
CONFIG_PATH="$HOST_DATA_DIR/config.json"

TELEGRAM_TOKEN="${TELEGRAM_TOKEN:-}"
PROVIDER="${PROVIDER:-openai}"
AGENT_URL="${AGENT_URL:-}"
AGENT_KEY="${AGENT_KEY:-}"
AGENT_MODEL="${AGENT_MODEL:-gpt-4o-mini}"
SYSTEM_PROMPT="${SYSTEM_PROMPT:-}"
WORKERS="${WORKERS:-4}"
QUEUE_SIZE="${QUEUE_SIZE:-64}"
POLL_TIMEOUT_SECOND="${POLL_TIMEOUT_SECOND:-25}"
REQUEST_TIMEOUT_SEC="${REQUEST_TIMEOUT_SEC:-60}"
RUNTIME_DATA_DIR="${DATA_DIR:-/app/data}"
HISTORY_TURNS="${HISTORY_TURNS:-8}"
AGENT_RETRY_COUNT="${AGENT_RETRY_COUNT:-2}"
SKILLS_SOURCE_DIR="${SKILLS_SOURCE_DIR:-/app/openclaw-skills}"
SKILLS_INSTALL_DIR="${SKILLS_INSTALL_DIR:-/app/data/skills}"
HEALTH_PORT="${HEALTH_PORT:-18080}"
RESTART_BACKOFF_MS="${RESTART_BACKOFF_MS:-1000}"
RESTART_MAX_MS="${RESTART_MAX_MS:-30000}"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi

docker compose version >/dev/null

if [ -n "${APP_VERSION:-}" ]; then
  BUILD_VERSION="$APP_VERSION"
else
  BUILD_VERSION="$(date -u +%Y%m%d.%H%M%S)"
fi
if [ -n "${APP_COMMIT:-}" ]; then
  BUILD_COMMIT="$APP_COMMIT"
elif [ -d "$ROOT_DIR/.git" ] && command -v git >/dev/null 2>&1; then
  BUILD_COMMIT="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
else
  BUILD_COMMIT=""
fi
if [ -z "$BUILD_COMMIT" ]; then
  BUILD_COMMIT="unknown"
fi
export APP_VERSION="$BUILD_VERSION"
export APP_COMMIT="$BUILD_COMMIT"

if [ -z "$AGENT_URL" ]; then
  case "$PROVIDER" in
    openai) AGENT_URL="https://api.openai.com/v1" ;;
    minimax) AGENT_URL="https://api.minimaxi.com/v1" ;;
    glm) AGENT_URL="https://open.bigmodel.cn/api/paas/v4" ;;
    custom) AGENT_URL="" ;;
    *)
      echo "unsupported PROVIDER: $PROVIDER" >&2
      exit 1
      ;;
  esac
fi

if [ -z "$TELEGRAM_TOKEN" ]; then
  printf "TELEGRAM_TOKEN: "
  IFS= read -r TELEGRAM_TOKEN
fi
if [ -z "$AGENT_KEY" ]; then
  printf "AGENT_KEY: "
  IFS= read -r AGENT_KEY
fi
if [ -z "$TELEGRAM_TOKEN" ] || [ -z "$AGENT_KEY" ]; then
  echo "TELEGRAM_TOKEN and AGENT_KEY are required" >&2
  exit 1
fi

mkdir -p "$HOST_DATA_DIR"
cat >"$CONFIG_PATH" <<EOF
{
  "telegram": {
    "bot_token": "$TELEGRAM_TOKEN"
  },
  "agent": {
    "provider": "$PROVIDER",
    "base_url": "$AGENT_URL",
    "api_key": "$AGENT_KEY",
    "model": "$AGENT_MODEL",
    "system_prompt": "$SYSTEM_PROMPT"
  },
  "runtime": {
    "workers": $WORKERS,
    "queue_size": $QUEUE_SIZE,
    "poll_timeout_second": $POLL_TIMEOUT_SECOND,
    "request_timeout_sec": $REQUEST_TIMEOUT_SEC,
    "data_dir": "$RUNTIME_DATA_DIR",
    "history_turns": $HISTORY_TURNS,
    "agent_retry_count": $AGENT_RETRY_COUNT,
    "skills_source_dir": "$SKILLS_SOURCE_DIR",
    "skills_install_dir": "$SKILLS_INSTALL_DIR",
    "health_port": $HEALTH_PORT,
    "restart_backoff_ms": $RESTART_BACKOFF_MS,
    "restart_max_ms": $RESTART_MAX_MS
  }
}
EOF

cd "$ROOT_DIR"
docker compose up -d --build --force-recreate

echo "Deploy complete."
echo "Build version: ${APP_VERSION} (${APP_COMMIT})"
echo "Config: $CONFIG_PATH"
echo "Health: http://127.0.0.1:${HEALTH_PORT}/healthz"

#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")"/.. && pwd)"

PULL_CODE="${PULL_CODE:-false}"
PRUNE_IMAGES="${PRUNE_IMAGES:-false}"

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

if [ "$PULL_CODE" = "true" ] && [ -d "$ROOT_DIR/.git" ]; then
  if ! command -v git >/dev/null 2>&1; then
    echo "git is required when PULL_CODE=true" >&2
    exit 1
  fi
  git -C "$ROOT_DIR" pull --ff-only
fi

cd "$ROOT_DIR"
docker compose up -d --build --remove-orphans --force-recreate

if [ "$PRUNE_IMAGES" = "true" ]; then
  docker image prune -f >/dev/null
fi

echo "Update complete."
echo "Build version: ${APP_VERSION} (${APP_COMMIT})"

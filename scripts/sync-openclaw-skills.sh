#!/usr/bin/env sh
set -eu

SOURCE_DIR="${1:-../openclaw/skills}"
TARGET_DIR="${2:-./openclaw-skills}"

if [ ! -d "$SOURCE_DIR" ]; then
  echo "source skills dir not found: $SOURCE_DIR" >&2
  exit 1
fi

mkdir -p "$TARGET_DIR"
cp -R "$SOURCE_DIR"/. "$TARGET_DIR"/

echo "synced skills:"
echo "  from: $SOURCE_DIR"
echo "  to:   $TARGET_DIR"

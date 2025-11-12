#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
DIST_DIR="$BIN_DIR/dist"
FRONTEND_DIR="$ROOT_DIR/frontend"

mkdir -p "$BIN_DIR"

# 构建 Go 后端
GOOS=${GOOS:-}
GOARCH=${GOARCH:-}
OUTPUT="$BIN_DIR/mybox"

echo "[+] 构建后端 -> $OUTPUT"
go build -o "$OUTPUT" "$ROOT_DIR/cmd/herobox"

# 准备前端静态资源
if [ -d "$DIST_DIR" ]; then
  rm -rf "$DIST_DIR"
fi
mkdir -p "$DIST_DIR"

echo "[+] 拷贝前端静态资源 -> $DIST_DIR"
cp -R "$FRONTEND_DIR"/* "$DIST_DIR"/

echo "[✓] 构建完成：$OUTPUT 与 $DIST_DIR"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DIR="$ROOT_DIR/bin"
DIST_DIR="$BIN_DIR/dist"
FRONTEND_DIR="$ROOT_DIR/frontend"
FRONTEND_DIST_DIR="$FRONTEND_DIR/dist"

mkdir -p "$BIN_DIR"

# 构建 Go 后端
GOOS=${GOOS:-}
GOARCH=${GOARCH:-}
OUTPUT="$BIN_DIR/mybox"

echo "[+] 构建后端 -> $OUTPUT"
go build -o "$OUTPUT" "$ROOT_DIR/cmd/herobox"

# 构建前端静态资源
if [ -d "$DIST_DIR" ]; then
  rm -rf "$DIST_DIR"
fi
mkdir -p "$DIST_DIR"

if [ -f "$FRONTEND_DIR/package.json" ]; then
  echo "[+] 构建前端 -> $FRONTEND_DIST_DIR"
  (cd "$FRONTEND_DIR" && npm install && npm run build)
  echo "[+] 拷贝前端构建产物 -> $DIST_DIR"
  cp -R "$FRONTEND_DIST_DIR"/* "$DIST_DIR"/
else
  echo "[!] 未检测到前端项目，直接拷贝静态资源"
  cp -R "$FRONTEND_DIR"/* "$DIST_DIR"/
fi

echo "[✓] 构建完成：$OUTPUT 与 $DIST_DIR"

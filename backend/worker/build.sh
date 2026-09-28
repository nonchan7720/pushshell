#!/usr/bin/env bash
# Builds ./build/worker.mjs + ./build/app.wasm from ../cmd/worker, for
# `wrangler dev` / `wrangler deploy` (wrangler.toml: main = "./build/worker.mjs").
#
# Run via `mise run worker:build` (sets up GOFLAGS=-mod=mod / the mise Go
# toolchain) rather than directly, unless you already have an equivalent Go
# environment on PATH.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

export GOFLAGS="${GOFLAGS:--mod=mod}"

# 1. wasm_exec.js + the JS glue (runtime.mjs / worker.mjs) that loads
#    ./app.wasm and forwards Workers fetch events into it.
go run github.com/syumai/workers-go/cmd/workers-assets-gen -mode=go -runtime=cloudflare -o ./build

# 2. The Go program itself, as wasm. -s -w strips debug info (there is no
#    step that would use it here) and -trimpath drops local build-machine
#    paths from the binary, both purely to shrink app.wasm toward the
#    Workers size limits (3MB gzip on the free plan, 10MB on paid).
GOOS=js GOARCH=wasm go build -ldflags='-s -w' -trimpath -o ./build/app.wasm ../cmd/worker

raw=$(wc -c < ./build/app.wasm)
gz=$(gzip -c ./build/app.wasm | wc -c)
echo "build/app.wasm: ${raw} bytes raw, ${gz} bytes gzip"

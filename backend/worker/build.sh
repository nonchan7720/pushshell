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
#    paths from the binary. Both are free, so they stay, but nothing here
#    squeezes the binary any further: Workers' only size limit is now 64 MiB
#    of *uncompressed* upload on every plan (there is no compressed-size
#    limit any more), and app.wasm is a small fraction of that.
GOOS=js GOARCH=wasm go build -ldflags='-s -w' -trimpath -o ./build/app.wasm ../cmd/worker

# Uncompressed total of everything wrangler uploads from ./build — the same
# number the size gate in .github/workflows/ci.yml checks against 64 MiB.
total=$(cat ./build/* | wc -c)
echo "build/: ${total} bytes uncompressed (app.wasm: $(wc -c < ./build/app.wasm)), limit 67108864 (64 MiB)"

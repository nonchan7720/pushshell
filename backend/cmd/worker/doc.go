//go:build !(js && wasm)

// Command worker is the Cloudflare Workers + D1 entrypoint for this
// notification backend: the same internal/handler, internal/server,
// internal/push and internal/ent as cmd/server, compiled to wasm
// (GOOS=js GOARCH=wasm) and served by github.com/syumai/workers-go instead
// of net/http's ListenAndServe. See main.go (built only for js/wasm) for the
// actual entrypoint, backend/worker/ for the wrangler project that packages
// it, and backend/README.md ("Cloudflare Workers + D1 で動かす") for setup.
//
// This file exists only so the cmd/worker package stays buildable on every
// other GOOS/GOARCH (`go build ./...`, `go vet ./...` on linux/darwin/...):
// main.go is entirely excluded outside js/wasm, and a `package main` with no
// files at all would fail to link ("function main is undeclared in the main
// package"). It intentionally does nothing.
package main

func main() {}

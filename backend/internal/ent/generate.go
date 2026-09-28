// Package ent はデータアクセス層。スキーマは schema/ 配下にあり、
// `go generate ./internal/ent/...` (mise run gen:ent) でコードを生成する。
package ent

//go:generate go tool ent generate --feature sql/upsert,sql/versioned-migration ./schema

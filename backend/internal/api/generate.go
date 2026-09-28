// Package api は openapi/openapi.yaml から oapi-codegen で生成される
// サーバーインターフェース・型・クライアントを提供する。
// 再生成: mise run gen:openapi:go
package api

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../../../openapi/openapi.yaml

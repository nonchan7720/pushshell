package handler

import "context"

// Authorizer は端末登録 (POST /v1/devices, DELETE /v1/devices/{id}) の認可を行う。
//
// Web アプリはログイン時に postMessage で任意のトークンを渡せる (bridge の `token`)。
// アプリはそれを Authorization: Bearer として送るので、ここで検証すれば
// 第三者が勝手に他人のログイン ID で端末を登録することを防げる。
// 既定の AllowAll は検証しない (開発用)。本番では Web アプリのセッション検証を実装すること。
type Authorizer interface {
	// AuthorizeDevice は loginID に対する token が正当なら nil を返す。
	// 認可しない場合は ErrUnauthorized (もしくはそれをラップしたエラー) を返す。
	AuthorizeDevice(ctx context.Context, loginID, token string) error
}

// AllowAll は全ての登録を許可する Authorizer。
type AllowAll struct{}

// AuthorizeDevice implements Authorizer.
func (AllowAll) AuthorizeDevice(context.Context, string, string) error { return nil }

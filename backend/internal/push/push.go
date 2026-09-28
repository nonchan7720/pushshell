// Package push はプッシュ通知の送信を抽象化する。
// 既定の実装は Expo Push API (expo.go)。FCM / APNs を直接使いたい場合は
// Sender を実装して server.New に渡す。
package push

import (
	"context"
	"log/slog"
)

// Message は 1 端末宛のプッシュ通知。
type Message struct {
	// To は Expo Push Token (ExponentPushToken[...])。
	To        string
	Title     string
	Body      string
	Data      map[string]any
	Sound     string
	Badge     *int
	ChannelID string
}

// Result は 1 通の送信結果。
type Result struct {
	OK bool
	// Error はプッシュサービスからのエラー内容 (OK=false のとき)。
	Error string
	// Unregistered はトークンが失効しており、端末を削除すべきなら true。
	Unregistered bool
}

// Sender はプッシュ通知の送信手段。
type Sender interface {
	// Send は messages と同じ長さ・順序の Result を返す。
	// 送信自体が失敗した場合 (ネットワーク等) はエラーを返す。
	Send(ctx context.Context, messages []Message) ([]Result, error)
}

// LogSender は実際には送信せずログに出す (開発 / テスト用)。
type LogSender struct {
	Logger *slog.Logger
}

// Send implements Sender.
func (s LogSender) Send(_ context.Context, messages []Message) ([]Result, error) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	results := make([]Result, len(messages))
	for i, m := range messages {
		logger.Info("push (log provider)", "to", m.To, "title", m.Title, "body", m.Body, "data", m.Data)
		results[i] = Result{OK: true}
	}
	return results, nil
}

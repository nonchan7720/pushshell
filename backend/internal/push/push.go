// Package push はプッシュ通知の送信を抽象化する。
// 既定の実装は Expo Push API (expo.go)。FCM (fcm.go) / APNs (apns.go) を
// 直接使いたい場合は NativeSender (native.go) を、独自のバックエンドを使い
// たい場合は Sender を実装して server.New に渡す。
package push

import (
	"context"
	"log/slog"
)

// Message は 1 端末宛のプッシュ通知。
type Message struct {
	// Platform は "ios" | "android"。NativeSender が Android/iOS の
	// どちらの Sender (FCM/APNs) に振り分けるかに使う。
	Platform string
	// ExpoToken は Expo Push Token (ExponentPushToken[...])。ExpoSender が使う。
	ExpoToken string
	// DeviceToken はネイティブのデバイストークン
	// (Android は FCM registration token、iOS は APNs device token の hex)。
	// FCMSender / APNSSender / NativeSender が使う。
	DeviceToken string
	Title       string
	Body        string
	Data        map[string]any
	Sound       string
	Badge       *int
	ChannelID   string

	// TTLSeconds は通知を保持する秒数 (0 〜 2419200)。nil なら未指定で、
	// 各サービスの既定値に従う。Expo は ttl、FCM は android.ttl ("<n>s")、
	// APNs は apns-expiration (現在時刻 + TTLSeconds の UNIX 秒) に対応する。
	TTLSeconds *int
	// Priority は配信優先度 "high" | "normal"。"" は high (従来どおり) 扱い。
	// Expo は priority、FCM は android.priority (high / normal)、
	// APNs は apns-priority (10 / 5) に対応する。
	Priority string
	// CollapseKey は同じキーの未配信の通知を 1 つにまとめるキー。
	// FCM は android.collapse_key、APNs は apns-collapse-id に対応する。
	// Expo は非対応のため無視する。
	CollapseKey string
	// Image は通知に表示する画像の URL (絶対 http(s) URL)。
	// Expo は richContent.image、FCM は notification.image に対応する。
	// APNs は Notification Service Extension なしでは表示できないため無視する。
	Image string
	// Silent が true ならサイレント通知 (Data だけを届けるデータのみの通知)。
	// Title / Body / Sound / Badge は使わない。Expo は _contentAvailable: true、
	// FCM は notification を省略、APNs は apns-push-type: background と
	// aps.content-available: 1 (apns-priority は Apple の要件により常に 5) で送る。
	Silent bool
	// Subtitle は通知のサブタイトル (iOS)。Expo は subtitle、APNs は
	// aps.alert.subtitle に対応する。FCM は非対応のため無視する。
	Subtitle string
	// ThreadID は iOS の通知グループ化 ID。APNs の aps.thread-id に対応する。
	// Expo と FCM は非対応のため無視する。
	ThreadID string
	// InterruptionLevel は iOS 15 以降の割り込みレベル
	// "passive" | "active" | "time-sensitive" | "critical"。
	// Expo は interruptionLevel、APNs は aps.interruption-level に対応する。
	// FCM は非対応のため無視する。
	InterruptionLevel string
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
		logger.Info("push (log provider)",
			"platform", m.Platform, "expoToken", m.ExpoToken, "deviceToken", m.DeviceToken,
			"title", m.Title, "body", m.Body, "data", m.Data,
			"ttlSeconds", ttlForLog(m.TTLSeconds), "priority", m.Priority, "collapseKey", m.CollapseKey,
			"image", m.Image, "silent", m.Silent, "subtitle", m.Subtitle, "threadId", m.ThreadID,
			"interruptionLevel", m.InterruptionLevel)
		results[i] = Result{OK: true}
	}
	return results, nil
}

// ttlForLog は TTLSeconds をログ用に値へ展開する (nil は "未指定" を表す -1)。
func ttlForLog(ttl *int) int {
	if ttl == nil {
		return -1
	}
	return *ttl
}

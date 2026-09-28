package push

import (
	"context"
	"fmt"
	"net/http"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
)

// apnsDefaultConcurrency は同時送信数の既定値。
const apnsDefaultConcurrency = 8

// apnsClient は APNSSender が必要とする *apns2.Client のサブセット
// (テストでフェイクに差し替えられるように interface にしている)。
type apnsClient interface {
	PushWithContext(ctx apns2.Context, n *apns2.Notification) (*apns2.Response, error)
}

// APNSEnvironment は APNs の接続先環境。
type APNSEnvironment string

// 対応環境。
const (
	APNSSandbox    APNSEnvironment = "sandbox"
	APNSProduction APNSEnvironment = "production"
)

// APNSSender は APNs (token-based / .p8 認証) 経由で iOS 端末に送信する Sender。
type APNSSender struct {
	// Client は下位の APNs クライアント。NewAPNSSender で作るか、テストでは
	// apnsClient を満たすフェイクを直接設定する。
	Client apnsClient
	// Topic は apns-topic ヘッダに使う値 (通常はアプリのバンドル ID)。
	Topic string
	// Concurrency は同時送信数 (省略時 apnsDefaultConcurrency)。
	Concurrency int
}

// NewAPNSSender は .p8 の Auth Key (PEM) から APNSSender を作る。
// keyID / teamID は Apple Developer の Keys ページ、topic はアプリのバンドル ID。
func NewAPNSSender(authKeyPEM []byte, keyID, teamID, topic string, env APNSEnvironment) (*APNSSender, error) {
	authKey, err := token.AuthKeyFromBytes(authKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("apns: parse auth key: %w", err)
	}
	tok := &token.Token{AuthKey: authKey, KeyID: keyID, TeamID: teamID}
	client := apns2.NewTokenClient(tok)
	// apns2.NewTokenClient always wires up an http2.Transport dialing TCP/TLS
	// directly, which does not exist in the Cloudflare Workers/wasm sandbox.
	// There (see httpclient_js.go), wasmTransport swaps in one backed by
	// cloudflare/fetch. On every other platform wasmTransport
	// (httpclient_default.go) returns nil and this is a no-op, so
	// cmd/server's behavior is unchanged.
	if t := wasmTransport(); t != nil {
		client.HTTPClient = &http.Client{Transport: t, Timeout: apns2.HTTPClientTimeout}
	}
	if env == APNSProduction {
		client = client.Production()
	} else {
		client = client.Development()
	}
	return &APNSSender{Client: client, Topic: topic}, nil
}

// Send implements Sender.
func (s *APNSSender) Send(ctx context.Context, messages []Message) ([]Result, error) {
	results := make([]Result, len(messages))
	concurrency := s.Concurrency
	if concurrency <= 0 {
		concurrency = apnsDefaultConcurrency
	}
	runPool(len(messages), concurrency, func(i int) {
		results[i] = s.sendOne(ctx, messages[i])
	})
	return results, nil
}

func (s *APNSSender) sendOne(ctx context.Context, m Message) Result {
	if m.DeviceToken == "" {
		return Result{Error: "device has no native token"}
	}
	if s.Client == nil {
		return Result{Error: "apns: no client configured"}
	}
	n := &apns2.Notification{
		DeviceToken: m.DeviceToken,
		Topic:       s.Topic,
		Priority:    apns2.PriorityHigh,
		PushType:    apns2.PushTypeAlert,
		Payload:     buildAPNSPayload(m),
	}
	resp, err := s.Client.PushWithContext(ctx, n)
	if err != nil {
		return Result{Error: fmt.Sprintf("apns: %v", err)}
	}
	return classifyAPNSResponse(resp)
}

// buildAPNSPayload builds the aps payload from a Message. Arbitrary Data
// entries are attached as custom top-level payload keys.
func buildAPNSPayload(m Message) *payload.Payload {
	p := payload.NewPayload().AlertTitle(m.Title).AlertBody(m.Body)
	if m.Sound != "" {
		p = p.Sound(m.Sound)
	}
	if m.Badge != nil {
		p = p.Badge(*m.Badge)
	}
	for k, v := range m.Data {
		p = p.Custom(k, v)
	}
	return p
}

// classifyAPNSResponse maps an APNs response to a Result. BadDeviceToken,
// Unregistered (410 Gone) and ExpiredToken all mean the device token is
// stale and the device should be dropped.
func classifyAPNSResponse(resp *apns2.Response) Result {
	if resp.Sent() {
		return Result{OK: true}
	}
	unregistered := resp.Reason == apns2.ReasonBadDeviceToken ||
		resp.Reason == apns2.ReasonUnregistered ||
		resp.Reason == apns2.ReasonExpiredToken ||
		resp.StatusCode == http.StatusGone
	return Result{Error: fmt.Sprintf("apns: %s", resp.Reason), Unregistered: unregistered}
}

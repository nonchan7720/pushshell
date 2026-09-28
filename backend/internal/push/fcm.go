package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// fcmScope は FCM HTTP v1 の送信に必要な OAuth2 スコープ。
const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// fcmEndpointFormat は FCM HTTP v1 のエンドポイント (%s は project id)。
const fcmEndpointFormat = "https://fcm.googleapis.com/v1/projects/%s/messages:send"

// fcmDefaultConcurrency は同時送信数の既定値。
const fcmDefaultConcurrency = 8

// FCMSender は FCM HTTP v1 経由で Android 端末に送信する Sender。
type FCMSender struct {
	// ProjectID は Firebase プロジェクト ID。
	ProjectID string
	// TokenSource は Authorization: Bearer に使うアクセストークンの供給元。
	// NewFCMSender で作るとキャッシュ付き (oauth2.ReuseTokenSource) になる。
	TokenSource oauth2.TokenSource
	// Endpoint は省略時 fcmEndpointFormat + ProjectID (テスト用の差し替え口)。
	Endpoint string
	// HTTPClient は省略時 10 秒タイムアウトのクライアント。
	HTTPClient *http.Client
	// Concurrency は同時送信数 (省略時 fcmDefaultConcurrency)。
	Concurrency int
}

// NewFCMSender は Firebase サービスアカウント JSON (Firebase コンソール →
// プロジェクトの設定 → サービスアカウント → 新しい秘密鍵の生成 でダウンロード
// できるファイル) から FCMSender を作る。projectID が空なら JSON 内の
// "project_id" を使う。
func NewFCMSender(serviceAccountJSON []byte, projectID string) (*FCMSender, error) {
	cfg, err := google.JWTConfigFromJSON(serviceAccountJSON, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse service account: %w", err)
	}
	if projectID == "" {
		var sa struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(serviceAccountJSON, &sa); err != nil {
			return nil, fmt.Errorf("fcm: parse service account: %w", err)
		}
		projectID = sa.ProjectID
	}
	if projectID == "" {
		return nil, fmt.Errorf("fcm: project id not found in service account JSON and none given (set FCM_PROJECT_ID)")
	}
	ctx := context.Background()
	if t := wasmTransport(); t != nil {
		// The JWT->access-token exchange below is itself an HTTP call,
		// separate from FCMSender.HTTPClient (used only for the actual send
		// in sendOne): golang.org/x/oauth2 reads its client from this
		// context (oauth2.HTTPClient), defaulting to http.DefaultClient,
		// which is exactly the client httpclient_js.go's doc explains does
		// not work from inside Cloudflare Workers.
		ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: t})
	}
	ts := oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))
	return &FCMSender{ProjectID: projectID, TokenSource: ts}, nil
}

type fcmSendRequest struct {
	Message fcmMessage `json:"message"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification *fcmNotification  `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	Android      *fcmAndroidConfig `json:"android,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type fcmAndroidConfig struct {
	Priority     string                  `json:"priority,omitempty"`
	Notification *fcmAndroidNotification `json:"notification,omitempty"`
}

type fcmAndroidNotification struct {
	ChannelID string `json:"channel_id,omitempty"`
	Sound     string `json:"sound,omitempty"`
}

type fcmErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// Send implements Sender.
func (s *FCMSender) Send(ctx context.Context, messages []Message) ([]Result, error) {
	results := make([]Result, len(messages))
	concurrency := s.Concurrency
	if concurrency <= 0 {
		concurrency = fcmDefaultConcurrency
	}
	runPool(len(messages), concurrency, func(i int) {
		results[i] = s.sendOne(ctx, messages[i])
	})
	return results, nil
}

func (s *FCMSender) sendOne(ctx context.Context, m Message) Result {
	if m.DeviceToken == "" {
		return Result{Error: "device has no native token"}
	}
	if s.TokenSource == nil {
		return Result{Error: "fcm: no token source configured"}
	}
	tok, err := s.TokenSource.Token()
	if err != nil {
		return Result{Error: fmt.Sprintf("fcm: token: %v", err)}
	}

	msg := fcmMessage{
		Token: m.DeviceToken,
		Data:  stringifyData(m.Data),
	}
	if m.Title != "" || m.Body != "" {
		msg.Notification = &fcmNotification{Title: m.Title, Body: m.Body}
	}
	android := &fcmAndroidConfig{Priority: "high"}
	if m.ChannelID != "" || m.Sound != "" {
		android.Notification = &fcmAndroidNotification{ChannelID: m.ChannelID, Sound: m.Sound}
	}
	msg.Android = android

	body, err := json.Marshal(fcmSendRequest{Message: msg})
	if err != nil {
		return Result{Error: fmt.Sprintf("fcm: marshal: %v", err)}
	}

	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf(fcmEndpointFormat, s.ProjectID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{Error: fmt.Sprintf("fcm: new request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	client := s.HTTPClient
	if client == nil {
		// wasmTransport is non-nil only in a js/wasm build (cmd/worker); see
		// httpclient_js.go for why plain net/http doesn't reach FCM from
		// inside Cloudflare Workers on its own.
		client = &http.Client{Transport: wasmTransport(), Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Error: fmt.Sprintf("fcm: request: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{Error: fmt.Sprintf("fcm: read response: %v", err)}
	}
	if resp.StatusCode == http.StatusOK {
		return Result{OK: true}
	}
	var errResp fcmErrorResponse
	_ = json.Unmarshal(raw, &errResp)
	return classifyFCMError(resp.StatusCode, errResp, raw)
}

// classifyFCMError maps an FCM HTTP v1 error response to a Result.
// A 404 with status UNREGISTERED, or a 400 with status INVALID_ARGUMENT
// (either reported directly or via the FcmError detail's errorCode), means
// the registration token is stale and the device should be dropped.
func classifyFCMError(statusCode int, resp fcmErrorResponse, raw []byte) Result {
	if resp.Error.Message == "" && resp.Error.Status == "" {
		return Result{Error: fmt.Sprintf("fcm: unexpected status %d: %s", statusCode, truncate(raw, 512))}
	}
	errorCode := ""
	for _, d := range resp.Error.Details {
		if d.ErrorCode != "" {
			errorCode = d.ErrorCode
		}
	}
	unregistered := (statusCode == http.StatusNotFound && (resp.Error.Status == "UNREGISTERED" || errorCode == "UNREGISTERED")) ||
		(statusCode == http.StatusBadRequest && (resp.Error.Status == "INVALID_ARGUMENT" || errorCode == "INVALID_ARGUMENT"))
	msg := resp.Error.Message
	if msg == "" {
		msg = resp.Error.Status
	}
	return Result{Error: fmt.Sprintf("fcm: %s", msg), Unregistered: unregistered}
}

// stringifyData converts an arbitrary data map into the string-only map FCM
// requires: strings pass through as-is, everything else is JSON-encoded.
func stringifyData(data map[string]any) map[string]string {
	if len(data) == 0 {
		return nil
	}
	out := make(map[string]string, len(data))
	for k, v := range data {
		if sv, ok := v.(string); ok {
			out[k] = sv
			continue
		}
		if b, err := json.Marshal(v); err == nil {
			out[k] = string(b)
		} else {
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

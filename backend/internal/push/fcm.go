package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// fcmScope は FCM HTTP v1 の送信に必要な OAuth2 スコープ。
const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// fcmEndpointFormat は FCM HTTP v1 のエンドポイント (%s は project id)。
const fcmEndpointFormat = "https://fcm.googleapis.com/v1/projects/%s/messages:send"

// fcmDefaultConcurrency は同時送信数の既定値。
const fcmDefaultConcurrency = 8

// fcmGrantType は Google の OAuth2 JWT bearer フロー
// (RFC 7523) 用の grant_type。
const fcmGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"

// fcmTokenExpirySkew は、期限ぴったりまで使い切らず少し手前で取り直すための
// マージン ("~1 min before expiry")。
const fcmTokenExpirySkew = time.Minute

// fcmTokenProvider は Authorization: Bearer に使うアクセストークンの供給元。
// *fcmServiceAccountTokenSource (NewFCMSender が作る、キャッシュ付きの実装)
// のほか、テストではフェイクに差し替えられる。
type fcmTokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// FCMSender は FCM HTTP v1 経由で Android 端末に送信する Sender。
type FCMSender struct {
	// ProjectID は Firebase プロジェクト ID。
	ProjectID string
	// TokenSource は Authorization: Bearer に使うアクセストークンの供給元。
	// NewFCMSender で作るとキャッシュ付きになる。
	TokenSource fcmTokenProvider
	// Endpoint は省略時 fcmEndpointFormat + ProjectID (テスト用の差し替え口)。
	Endpoint string
	// HTTPClient は省略時 10 秒タイムアウトのクライアント (newHTTPClient)。
	HTTPClient HTTPDoer
	// Concurrency は同時送信数 (省略時 fcmDefaultConcurrency)。
	Concurrency int
}

// fcmServiceAccount は Firebase サービスアカウント JSON のうち、
// この実装が使うフィールドだけを取り出す。
type fcmServiceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
	ProjectID   string `json:"project_id"`
}

// NewFCMSender は Firebase サービスアカウント JSON (Firebase コンソール →
// プロジェクトの設定 → サービスアカウント → 新しい秘密鍵の生成 でダウンロード
// できるファイル) から FCMSender を作る。projectID が空なら JSON 内の
// "project_id" を使う。
//
// golang.org/x/oauth2/google には頼らず、サービスアカウントの秘密鍵で自前で
// JWT (RS256) を組み立てて token_uri に対する JWT bearer 交換
// (RFC 7523) を行う。取得したアクセストークンは期限の ~1 分前まで
// キャッシュする (fcmServiceAccountTokenSource)。
func NewFCMSender(serviceAccountJSON []byte, projectID string) (*FCMSender, error) {
	var sa fcmServiceAccount
	if err := json.Unmarshal(serviceAccountJSON, &sa); err != nil {
		return nil, fmt.Errorf("fcm: parse service account: %w", err)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" || sa.TokenURI == "" {
		return nil, fmt.Errorf("fcm: parse service account: client_email/private_key/token_uri missing")
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("fcm: parse service account: private_key is not a valid PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse service account: %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("fcm: parse service account: private_key is not an RSA key")
	}
	if projectID == "" {
		projectID = sa.ProjectID
	}
	if projectID == "" {
		return nil, fmt.Errorf("fcm: project id not found in service account JSON and none given (set FCM_PROJECT_ID)")
	}
	ts := &fcmServiceAccountTokenSource{
		clientEmail: sa.ClientEmail,
		privateKey:  rsaKey,
		tokenURI:    sa.TokenURI,
		// This is the HTTP client used for the JWT->access-token exchange
		// itself, separate from FCMSender.HTTPClient (used only for the
		// actual send in sendOne). See httpclient_js.go for what it is in
		// the js/wasm build (cmd/worker).
		httpClient: newHTTPClient(defaultHTTPTimeout),
	}
	return &FCMSender{ProjectID: projectID, TokenSource: ts}, nil
}

// fcmServiceAccountTokenSource is the fcmTokenProvider NewFCMSender builds:
// it signs a fresh RS256 JWT and exchanges it for an access token against
// tokenURI on demand, caching the result until ~1 min before it expires.
type fcmServiceAccountTokenSource struct {
	clientEmail string
	privateKey  *rsa.PrivateKey
	tokenURI    string
	httpClient  HTTPDoer

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// Token implements fcmTokenProvider.
func (ts *fcmServiceAccountTokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.token != "" && time.Now().Before(ts.expiry) {
		return ts.token, nil
	}
	token, expiry, err := ts.fetch(ctx)
	if err != nil {
		return "", err
	}
	ts.token = token
	ts.expiry = expiry
	return token, nil
}

func (ts *fcmServiceAccountTokenSource) fetch(ctx context.Context) (string, time.Time, error) {
	now := time.Now()
	assertion, err := signFCMAssertion(ts.clientEmail, ts.tokenURI, ts.privateKey, now)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fcm: sign assertion: %w", err)
	}
	form := url.Values{
		"grant_type": {fcmGrantType},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fcm: new token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := ts.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fcm: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fcm: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("fcm: token endpoint returned %d: %s", resp.StatusCode, truncate(raw, 512))
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("fcm: decode token response: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("fcm: token endpoint response has no access_token")
	}
	expiresIn := time.Duration(parsed.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}
	expiry := now.Add(expiresIn - fcmTokenExpirySkew)
	return parsed.AccessToken, expiry, nil
}

// signFCMAssertion builds and signs (RS256) the JWT used as the "assertion"
// in the JWT bearer token exchange (RFC 7523): header {"alg":"RS256",
// "typ":"JWT"}, claims iss=clientEmail, scope=fcmScope, aud=tokenURI,
// iat=now, exp=iat+1h.
func signFCMAssertion(clientEmail, tokenURI string, key *rsa.PrivateKey, now time.Time) (string, error) {
	header, err := jwtEncodeSegment(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := jwtEncodeSegment(map[string]any{
		"iss":   clientEmail,
		"scope": fcmScope,
		"aud":   tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + claims
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
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
	Image string `json:"image,omitempty"`
}

type fcmAndroidConfig struct {
	Priority     string                  `json:"priority,omitempty"`
	TTL          string                  `json:"ttl,omitempty"`
	CollapseKey  string                  `json:"collapse_key,omitempty"`
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
	accessToken, err := s.TokenSource.Token(ctx)
	if err != nil {
		return Result{Error: fmt.Sprintf("fcm: token: %v", err)}
	}

	msg := buildFCMMessage(m)

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
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := s.HTTPClient
	if client == nil {
		client = newHTTPClient(defaultHTTPTimeout)
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

// buildFCMMessage は Message を FCM HTTP v1 の message にする。
// Subtitle / ThreadID / InterruptionLevel は FCM (Android) では使えないため
// 無視する。Silent のときは notification を付けず (data のみ)、
// android.priority は通常どおり (既定 "high") 計算する。
func buildFCMMessage(m Message) fcmMessage {
	msg := fcmMessage{
		Token: m.DeviceToken,
		Data:  stringifyData(m.Data),
	}
	if !m.Silent && (m.Title != "" || m.Body != "" || m.Image != "") {
		msg.Notification = &fcmNotification{Title: m.Title, Body: m.Body, Image: m.Image}
	}
	// FCM HTTP v1 のドキュメントに合わせて小文字の "high" / "normal" を使う。
	// Priority "" は従来どおり "high" (既存のペイロードを 1 バイトも変えない)。
	priority := "high"
	if m.Priority == "normal" {
		priority = "normal"
	}
	android := &fcmAndroidConfig{Priority: priority, CollapseKey: m.CollapseKey}
	if m.TTLSeconds != nil {
		android.TTL = fmt.Sprintf("%ds", *m.TTLSeconds)
	}
	if !m.Silent && (m.ChannelID != "" || m.Sound != "") {
		android.Notification = &fcmAndroidNotification{ChannelID: m.ChannelID, Sound: m.Sound}
	}
	msg.Android = android
	return msg
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

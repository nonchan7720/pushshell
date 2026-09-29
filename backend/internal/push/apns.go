package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// apnsDefaultConcurrency は同時送信数の既定値。
const apnsDefaultConcurrency = 8

// apnsTokenTTL は APNs provider token を使い回す期間 ("~50 min").
// Apple は 1 時間以内のトークンを要求するので、少し手前で取り直す。
const apnsTokenTTL = 50 * time.Minute

// apnsProductionHost / apnsSandboxHost は APNs HTTP/2 API のホスト。
const (
	apnsProductionHost = "https://api.push.apple.com"
	apnsSandboxHost    = "https://api.sandbox.push.apple.com"
)

// APNSEnvironment は APNs の接続先環境。
type APNSEnvironment string

// 対応環境。
const (
	APNSSandbox    APNSEnvironment = "sandbox"
	APNSProduction APNSEnvironment = "production"
)

// APNSSender は APNs (token-based / .p8 認証) 経由で iOS 端末に送信する Sender。
type APNSSender struct {
	// HTTPClient は省略時 10 秒タイムアウトのクライアント (newHTTPClient)。
	// net/http の *http.Transport は https に対して自動で HTTP/2 を
	// ネゴシエートするので golang.org/x/net/http2 を明示的に使う必要はない
	// (wasm ビルドでは httpclient_js.go の fetch トランスポートが HTTP/2 を担う)。
	HTTPClient HTTPDoer
	// Endpoint は APNs のホスト (省略時 NewAPNSSender が渡した env に応じて
	// apnsProductionHost/apnsSandboxHost。テスト用の差し替え口)。
	Endpoint string
	// Topic は apns-topic ヘッダに使う値 (通常はアプリのバンドル ID)。
	Topic string
	// Concurrency は同時送信数 (省略時 apnsDefaultConcurrency)。
	Concurrency int

	// now は現在時刻の供給元 (省略時 time.Now)。apns-expiration の計算に使う。
	// テストで決定的にするための差し替え口。
	now func() time.Time

	// tokenSource は authorization ヘッダに使う JWT provider token の
	// 供給元。NewAPNSSender で作るとキャッシュ付きになる。テストでは
	// 同一パッケージ内から直接差し替える。
	tokenSource *apnsTokenSource
}

// NewAPNSSender は .p8 の Auth Key (PEM, PKCS#8 ECDSA P-256) から
// APNSSender を作る。keyID / teamID は Apple Developer の Keys ページ、
// topic はアプリのバンドル ID。
//
// github.com/sideshow/apns2 には頼らず、.p8 の秘密鍵で自前で JWT (ES256) を
// 組み立てて署名する (RFC 7519 / Apple の "Establishing a Token-Based
// Connection to APNs")。取得したトークンは ~50 分キャッシュする
// (apnsTokenSource)。
func NewAPNSSender(authKeyPEM []byte, keyID, teamID, topic string, env APNSEnvironment) (*APNSSender, error) {
	block, _ := pem.Decode(authKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("apns: parse auth key: not a valid PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns: parse auth key: %w", err)
	}
	ecKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || ecKey.Curve != elliptic.P256() {
		return nil, fmt.Errorf("apns: parse auth key: not a P-256 ECDSA key")
	}
	host := apnsProductionHost
	if env == APNSSandbox {
		host = apnsSandboxHost
	}
	return &APNSSender{
		HTTPClient:  newHTTPClient(defaultHTTPTimeout),
		Endpoint:    host,
		Topic:       topic,
		tokenSource: &apnsTokenSource{teamID: teamID, keyID: keyID, key: ecKey},
	}, nil
}

// apnsTokenSource builds and caches the ES256 APNs provider token used as
// the "bearer" credential in the authorization header.
type apnsTokenSource struct {
	teamID string
	keyID  string
	key    *ecdsa.PrivateKey

	mu          sync.Mutex
	token       string
	generatedAt time.Time
}

func (ts *apnsTokenSource) Token() (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.token != "" && time.Since(ts.generatedAt) < apnsTokenTTL {
		return ts.token, nil
	}
	now := time.Now()
	tok, err := signAPNSToken(ts.keyID, ts.teamID, ts.key, now)
	if err != nil {
		return "", err
	}
	ts.token = tok
	ts.generatedAt = now
	return tok, nil
}

// signAPNSToken builds and signs (ES256) the APNs provider token: header
// {"alg":"ES256","kid":keyID}, claims iss=teamID, iat=now. The ES256
// signature is the raw 64-byte R||S concatenation (32 bytes each, zero
// padded), base64url-encoded, per RFC 7518 §3.4 -- not the ASN.1 DER form
// crypto/ecdsa.Sign's (*big.Int, *big.Int) would naively produce.
func signAPNSToken(keyID, teamID string, key *ecdsa.PrivateKey, now time.Time) (string, error) {
	header, err := jwtEncodeSegment(map[string]string{"alg": "ES256", "kid": keyID})
	if err != nil {
		return "", err
	}
	claims, err := jwtEncodeSegment(map[string]any{
		"iss": teamID,
		"iat": now.Unix(),
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + claims
	sum := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
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
	if s.tokenSource == nil {
		return Result{Error: "apns: no token source configured"}
	}
	tok, err := s.tokenSource.Token()
	if err != nil {
		return Result{Error: fmt.Sprintf("apns: token: %v", err)}
	}

	body, err := json.Marshal(buildAPNSPayload(m))
	if err != nil {
		return Result{Error: fmt.Sprintf("apns: marshal: %v", err)}
	}

	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = apnsProductionHost
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/3/device/"+m.DeviceToken, bytes.NewReader(body))
	if err != nil {
		return Result{Error: fmt.Sprintf("apns: new request: %v", err)}
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "bearer "+tok)
	req.Header.Set("apns-topic", s.Topic)
	s.setAPNSHeaders(req, m)

	client := s.HTTPClient
	if client == nil {
		client = newHTTPClient(defaultHTTPTimeout)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Error: fmt.Sprintf("apns: request: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{Error: fmt.Sprintf("apns: read response: %v", err)}
	}
	if resp.StatusCode == http.StatusOK {
		return Result{OK: true}
	}
	var errResp struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &errResp)
	return classifyAPNSResponse(resp.StatusCode, errResp.Reason)
}

// setAPNSHeaders は apns-push-type / apns-priority / apns-expiration /
// apns-collapse-id を設定する。何も指定がなければ従来どおり
// apns-push-type: alert と apns-priority: 10 だけになる。
//
//   - Silent: apns-push-type: background、apns-priority: 5 (Apple は background に 5 を要求する)。
//   - Priority "normal": apns-priority: 5。それ以外は 10。
//   - TTLSeconds: apns-expiration = 現在時刻 + TTLSeconds の UNIX 秒 (0 は「即時失効」= 1 回だけ配信を試みる)。
//   - CollapseKey: apns-collapse-id。
func (s *APNSSender) setAPNSHeaders(req *http.Request, m Message) {
	pushType, priority := "alert", "10"
	if m.Silent {
		pushType, priority = "background", "5"
	} else if m.Priority == "normal" {
		priority = "5"
	}
	req.Header.Set("apns-push-type", pushType)
	req.Header.Set("apns-priority", priority)
	if m.TTLSeconds != nil {
		now := time.Now
		if s.now != nil {
			now = s.now
		}
		if *m.TTLSeconds == 0 {
			req.Header.Set("apns-expiration", "0")
		} else {
			req.Header.Set("apns-expiration", strconv.FormatInt(now().Unix()+int64(*m.TTLSeconds), 10))
		}
	}
	if m.CollapseKey != "" {
		req.Header.Set("apns-collapse-id", m.CollapseKey)
	}
}

// apnsAlert / apnsAps / apnsPayload mirror the shape Apple's APNs API
// expects under "aps"; buildAPNSPayload attaches arbitrary Message.Data
// entries as custom top-level payload keys alongside "aps".
type apnsAlert struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Subtitle string `json:"subtitle,omitempty"`
}

type apnsAps struct {
	Alert             *apnsAlert `json:"alert,omitempty"`
	Sound             string     `json:"sound,omitempty"`
	Badge             *int       `json:"badge,omitempty"`
	ContentAvailable  int        `json:"content-available,omitempty"`
	ThreadID          string     `json:"thread-id,omitempty"`
	InterruptionLevel string     `json:"interruption-level,omitempty"`
}

// buildAPNSPayload builds the aps payload from a Message. Arbitrary Data
// entries are attached as custom top-level payload keys. Image は APNs では
// (Notification Service Extension なしでは) 使えないため無視する。Silent の
// ときは alert / sound / badge を付けず aps.content-available: 1 だけにする。
func buildAPNSPayload(m Message) map[string]any {
	aps := apnsAps{}
	if m.Silent {
		aps.ContentAvailable = 1
	} else {
		aps.Alert = &apnsAlert{Title: m.Title, Body: m.Body, Subtitle: m.Subtitle}
		aps.Sound = m.Sound
		aps.Badge = m.Badge
		aps.ThreadID = m.ThreadID
		aps.InterruptionLevel = m.InterruptionLevel
	}
	p := map[string]any{"aps": aps}
	for k, v := range m.Data {
		p[k] = v
	}
	return p
}

// classifyAPNSResponse maps an APNs HTTP response to a Result.
// BadDeviceToken, Unregistered (410 Gone) and ExpiredToken all mean the
// device token is stale and the device should be dropped.
func classifyAPNSResponse(statusCode int, reason string) Result {
	if statusCode == http.StatusOK {
		return Result{OK: true}
	}
	unregistered := reason == "BadDeviceToken" ||
		reason == "Unregistered" ||
		reason == "ExpiredToken" ||
		statusCode == http.StatusGone
	msg := reason
	if msg == "" {
		msg = fmt.Sprintf("http %d", statusCode)
	}
	return Result{Error: fmt.Sprintf("apns: %s", msg), Unregistered: unregistered}
}

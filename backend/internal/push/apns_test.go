package push

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newTestAPNSServer starts an httptest HTTPS server with HTTP/2 enabled,
// and returns it plus an *http.Client (srv.Client()) that trusts its
// certificate -- mirroring how a real APNs connection is HTTP/2 over TLS.
func newTestAPNSServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func newTestAPNSKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// verifyES256JWT parses a two-dot JWT and verifies its ES256 (raw R||S)
// signature against pub, returning the decoded header and claims.
func verifyES256JWT(t *testing.T, jwt string, pub *ecdsa.PublicKey) (header, claims map[string]any) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed JWT (want 3 segments): %q", jwt)
	}
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("expected 64-byte raw R||S signature, got %d bytes", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	sum := sha256.Sum256([]byte(signingInput))
	if !ecdsa.Verify(pub, sum[:], r, s) {
		t.Fatal("ES256 signature verification failed")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return header, claims
}

func newTestAPNSSender(t *testing.T, srv *httptest.Server, key *ecdsa.PrivateKey) *APNSSender {
	t.Helper()
	return &APNSSender{
		HTTPClient:  srv.Client(),
		Endpoint:    srv.URL,
		Topic:       "com.example.app",
		tokenSource: &apnsTokenSource{teamID: "TEAM123", keyID: "KEY123", key: key},
	}
}

func TestAPNSSender_Send_OK(t *testing.T) {
	key := newTestAPNSKey(t)
	var gotPath string
	var gotHeaders http.Header
	var gotBody []byte
	srv := newTestAPNSServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		gotBody = b
		w.WriteHeader(http.StatusOK)
	})

	s := newTestAPNSSender(t, srv, key)
	badge := 5
	res, err := s.Send(context.Background(), []Message{
		{
			Platform:    "ios",
			DeviceToken: "abc123",
			Title:       "t",
			Body:        "b",
			Sound:       "default",
			Badge:       &badge,
			Data:        map[string]any{"url": "https://x"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("unexpected result: %+v", res)
	}
	if gotPath != "/3/device/abc123" {
		t.Fatalf("unexpected path: %q", gotPath)
	}
	if got := gotHeaders.Get("apns-topic"); got != "com.example.app" {
		t.Fatalf("unexpected apns-topic: %q", got)
	}
	if got := gotHeaders.Get("apns-push-type"); got != "alert" {
		t.Fatalf("unexpected apns-push-type: %q", got)
	}
	if got := gotHeaders.Get("apns-priority"); got != "10" {
		t.Fatalf("unexpected apns-priority: %q", got)
	}
	auth := gotHeaders.Get("authorization")
	if !strings.HasPrefix(auth, "bearer ") {
		t.Fatalf("unexpected authorization header: %q", auth)
	}
	header, claims := verifyES256JWT(t, strings.TrimPrefix(auth, "bearer "), &key.PublicKey)
	if header["alg"] != "ES256" || header["kid"] != "KEY123" {
		t.Fatalf("unexpected JWT header: %+v", header)
	}
	if claims["iss"] != "TEAM123" {
		t.Fatalf("unexpected JWT claims: %+v", claims)
	}
	if _, ok := claims["iat"]; !ok {
		t.Fatalf("expected iat claim: %+v", claims)
	}

	var decoded struct {
		Aps struct {
			Alert struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"alert"`
			Sound string `json:"sound"`
			Badge int    `json:"badge"`
		} `json:"aps"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("decode payload: %v (body: %s)", err, gotBody)
	}
	if decoded.Aps.Alert.Title != "t" || decoded.Aps.Alert.Body != "b" || decoded.Aps.Sound != "default" || decoded.Aps.Badge != 5 {
		t.Fatalf("unexpected payload: %s", gotBody)
	}
	if decoded.URL != "https://x" {
		t.Fatalf("expected custom data key at top level: %s", gotBody)
	}
}

func TestAPNSSender_Send_NoDeviceToken(t *testing.T) {
	key := newTestAPNSKey(t)
	called := false
	srv := newTestAPNSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	s := newTestAPNSSender(t, srv, key)
	res, err := s.Send(context.Background(), []Message{{}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Error != "device has no native token" {
		t.Fatalf("unexpected result: %+v", res[0])
	}
	if called {
		t.Fatal("must not call APNs without a device token")
	}
}

func TestAPNSSender_Send_Unregistered410(t *testing.T) {
	key := newTestAPNSKey(t)
	srv := newTestAPNSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"reason":"Unregistered","timestamp":1234567890}`))
	})
	s := newTestAPNSSender(t, srv, key)
	res, err := s.Send(context.Background(), []Message{{DeviceToken: "dead"}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || !res[0].Unregistered {
		t.Fatalf("expected unregistered result, got %+v", res[0])
	}
}

func TestAPNSSender_Send_BadDeviceToken(t *testing.T) {
	key := newTestAPNSKey(t)
	srv := newTestAPNSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"BadDeviceToken"}`))
	})
	s := newTestAPNSSender(t, srv, key)
	res, err := s.Send(context.Background(), []Message{{DeviceToken: "bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || !res[0].Unregistered {
		t.Fatalf("expected unregistered result, got %+v", res[0])
	}
}

func TestAPNSSender_Send_OtherError(t *testing.T) {
	key := newTestAPNSKey(t)
	srv := newTestAPNSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"BadTopic"}`))
	})
	s := newTestAPNSSender(t, srv, key)
	res, err := s.Send(context.Background(), []Message{{DeviceToken: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Unregistered || res[0].Error == "" {
		t.Fatalf("expected non-unregistered error result, got %+v", res[0])
	}
}

func TestClassifyAPNSResponse(t *testing.T) {
	cases := []struct {
		name         string
		statusCode   int
		reason       string
		wantOK       bool
		wantUnreg    bool
		wantErrEmpty bool
	}{
		{"sent", http.StatusOK, "", true, false, true},
		{"bad device token", http.StatusBadRequest, "BadDeviceToken", false, true, false},
		{"unregistered", http.StatusGone, "Unregistered", false, true, false},
		{"expired token", http.StatusBadRequest, "ExpiredToken", false, true, false},
		{"other error", http.StatusBadRequest, "BadTopic", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := classifyAPNSResponse(c.statusCode, c.reason)
			if r.OK != c.wantOK {
				t.Fatalf("OK: got %v want %v", r.OK, c.wantOK)
			}
			if r.Unregistered != c.wantUnreg {
				t.Fatalf("Unregistered: got %v want %v", r.Unregistered, c.wantUnreg)
			}
			if c.wantErrEmpty && r.Error != "" {
				t.Fatalf("expected empty error, got %q", r.Error)
			}
			if !c.wantErrEmpty && r.Error == "" {
				t.Fatal("expected non-empty error")
			}
		})
	}
}

func TestAPNSSender_Send_PreservesOrder(t *testing.T) {
	key := newTestAPNSKey(t)
	srv := newTestAPNSServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/device/bad-3" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"reason":"BadDeviceToken"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	s := newTestAPNSSender(t, srv, key)
	s.Concurrency = 3
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{DeviceToken: "ok"}
	}
	msgs[3].DeviceToken = "bad-3"

	res, err := s.Send(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range res {
		if i == 3 {
			if r.OK || !r.Unregistered {
				t.Fatalf("index 3: expected unregistered, got %+v", r)
			}
			continue
		}
		if !r.OK {
			t.Fatalf("index %d: expected ok, got %+v", i, r)
		}
	}
}

func TestApnsTokenSource_Caches(t *testing.T) {
	key := newTestAPNSKey(t)
	ts := &apnsTokenSource{teamID: "TEAM123", keyID: "KEY123", key: key}
	tok1, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != tok2 {
		t.Fatalf("expected cached token to be reused: %q != %q", tok1, tok2)
	}
}

func TestApnsTokenSource_ConcurrentSafe(t *testing.T) {
	key := newTestAPNSKey(t)
	ts := &apnsTokenSource{teamID: "TEAM123", keyID: "KEY123", key: key}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ts.Token(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestNewAPNSSender_InvalidKey(t *testing.T) {
	if _, err := NewAPNSSender([]byte("not a pem"), "kid", "tid", "topic", APNSProduction); err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestNewAPNSSender_ParsesP8AndSetsEndpoint(t *testing.T) {
	key := newTestAPNSKey(t)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	prod, err := NewAPNSSender(p8, "kid", "tid", "com.example.app", APNSProduction)
	if err != nil {
		t.Fatal(err)
	}
	if prod.Endpoint != apnsProductionHost {
		t.Fatalf("unexpected production endpoint: %q", prod.Endpoint)
	}
	if prod.Topic != "com.example.app" {
		t.Fatalf("unexpected topic: %q", prod.Topic)
	}
	if prod.HTTPClient == nil {
		t.Fatal("expected non-nil HTTPClient")
	}

	sandbox, err := NewAPNSSender(p8, "kid", "tid", "com.example.app", APNSSandbox)
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.Endpoint != apnsSandboxHost {
		t.Fatalf("unexpected sandbox endpoint: %q", sandbox.Endpoint)
	}
}

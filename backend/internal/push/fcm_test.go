package push

import (
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
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeTokenSource returns a fixed access token without any network call.
type fakeTokenSource struct {
	accessToken string
	err         error
}

func (f fakeTokenSource) Token(context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.accessToken, nil
}

func TestFCMSender_Send_OK(t *testing.T) {
	var gotAuth string
	var gotBody fcmSendRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"projects/p/messages/1"}`))
	}))
	defer srv.Close()

	s := &FCMSender{
		ProjectID:   "p",
		TokenSource: fakeTokenSource{accessToken: "secret"},
		Endpoint:    srv.URL,
	}
	badge := 2
	res, err := s.Send(context.Background(), []Message{
		{
			Platform:    "android",
			DeviceToken: "device-1",
			Title:       "t",
			Body:        "b",
			ChannelID:   "default",
			Sound:       "default",
			Badge:       &badge,
			Data:        map[string]any{"url": "https://x", "count": 3, "flag": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("unexpected results: %+v", res)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("unexpected Authorization header: %q", gotAuth)
	}
	if gotBody.Message.Token != "device-1" {
		t.Fatalf("unexpected token: %+v", gotBody.Message)
	}
	if gotBody.Message.Notification == nil || gotBody.Message.Notification.Title != "t" || gotBody.Message.Notification.Body != "b" {
		t.Fatalf("unexpected notification: %+v", gotBody.Message.Notification)
	}
	if gotBody.Message.Android == nil || gotBody.Message.Android.Priority != "high" {
		t.Fatalf("unexpected android config: %+v", gotBody.Message.Android)
	}
	if gotBody.Message.Android.Notification == nil || gotBody.Message.Android.Notification.ChannelID != "default" || gotBody.Message.Android.Notification.Sound != "default" {
		t.Fatalf("unexpected android notification: %+v", gotBody.Message.Android.Notification)
	}
	if gotBody.Message.Data["url"] != "https://x" || gotBody.Message.Data["count"] != "3" || gotBody.Message.Data["flag"] != "true" {
		t.Fatalf("unexpected data (should be stringified): %+v", gotBody.Message.Data)
	}
}

func TestFCMSender_Send_Unregistered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
	}))
	defer srv.Close()

	s := &FCMSender{ProjectID: "p", TokenSource: fakeTokenSource{accessToken: "t"}, Endpoint: srv.URL}
	res, err := s.Send(context.Background(), []Message{{DeviceToken: "dead"}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || !res[0].Unregistered {
		t.Fatalf("expected unregistered result, got %+v", res[0])
	}
}

func TestFCMSender_Send_InvalidArgument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"bad token","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()

	s := &FCMSender{ProjectID: "p", TokenSource: fakeTokenSource{accessToken: "t"}, Endpoint: srv.URL}
	res, err := s.Send(context.Background(), []Message{{DeviceToken: "bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || !res[0].Unregistered {
		t.Fatalf("expected unregistered result, got %+v", res[0])
	}
}

func TestFCMSender_Send_OtherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"internal","status":"INTERNAL"}}`))
	}))
	defer srv.Close()

	s := &FCMSender{ProjectID: "p", TokenSource: fakeTokenSource{accessToken: "t"}, Endpoint: srv.URL}
	res, err := s.Send(context.Background(), []Message{{DeviceToken: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Unregistered || res[0].Error == "" {
		t.Fatalf("expected non-unregistered error result, got %+v", res[0])
	}
}

func TestFCMSender_Send_NoDeviceToken(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	s := &FCMSender{ProjectID: "p", TokenSource: fakeTokenSource{accessToken: "t"}, Endpoint: srv.URL}
	res, err := s.Send(context.Background(), []Message{{}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Error != "device has no native token" {
		t.Fatalf("unexpected result: %+v", res[0])
	}
	if called {
		t.Fatal("must not call FCM without a device token")
	}
}

func TestFCMSender_Send_PreservesOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body fcmSendRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Message.Token == "bad-3" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	s := &FCMSender{ProjectID: "p", TokenSource: fakeTokenSource{accessToken: "t"}, Endpoint: srv.URL, Concurrency: 3}
	msgs := make([]Message, 20)
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

// --- NewFCMSender / token exchange / JWT signing ---

// newTestServiceAccountJSON builds a Firebase-service-account-shaped JSON
// document, with its private_key PEM (PKCS#8) wrapping key, pointing
// token_uri at tokenURI.
func newTestServiceAccountJSON(t *testing.T, key *rsa.PrivateKey, tokenURI string) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	sa := fcmServiceAccount{
		ClientEmail: "test@example-project.iam.gserviceaccount.com",
		PrivateKey:  string(pemBytes),
		TokenURI:    tokenURI,
		ProjectID:   "example-project",
	}
	b, err := json.Marshal(sa)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// verifyRS256JWT parses a two-dot JWT and verifies its RS256 signature
// against pub, returning the decoded claims.
func verifyRS256JWT(t *testing.T, jwt string, pub *rsa.PublicKey) map[string]any {
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
	sum := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("RS256 signature verification failed: %v", err)
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

func TestNewFCMSender_TokenExchangeAndCaching(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	var tokenCalls int32
	mux := http.NewServeMux()
	var sendCalls int32
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.FormValue("grant_type"); got != fcmGrantType {
			t.Errorf("unexpected grant_type: %q", got)
		}
		assertion := r.FormValue("assertion")
		claims := verifyRS256JWT(t, assertion, &key.PublicKey)
		if claims["iss"] != "test@example-project.iam.gserviceaccount.com" {
			t.Errorf("unexpected iss: %v", claims["iss"])
		}
		if claims["scope"] != fcmScope {
			t.Errorf("unexpected scope: %v", claims["scope"])
		}
		if claims["aud"] != srv.URL+"/token" {
			t.Errorf("unexpected aud: %v", claims["aud"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"tok-%d","expires_in":3600,"token_type":"Bearer"}`, atomic.LoadInt32(&tokenCalls))
	})
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sendCalls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer tok-1" {
			t.Errorf("unexpected Authorization (want cached token): %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	saJSON := newTestServiceAccountJSON(t, key, srv.URL+"/token")
	sender, err := NewFCMSender(saJSON, "")
	if err != nil {
		t.Fatal(err)
	}
	if sender.ProjectID != "example-project" {
		t.Fatalf("unexpected ProjectID: %q", sender.ProjectID)
	}
	sender.Endpoint = srv.URL + "/send"

	for i := range 2 {
		res, err := sender.Send(context.Background(), []Message{{DeviceToken: fmt.Sprintf("dev-%d", i)}})
		if err != nil {
			t.Fatal(err)
		}
		if !res[0].OK {
			t.Fatalf("send %d: unexpected result: %+v", i, res[0])
		}
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Fatalf("expected exactly 1 token exchange (token should be cached/reused), got %d", got)
	}
	if got := atomic.LoadInt32(&sendCalls); got != 2 {
		t.Fatalf("expected 2 send calls, got %d", got)
	}
}

func TestNewFCMSender_ExplicitProjectIDOverridesJSON(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	saJSON := newTestServiceAccountJSON(t, key, "https://example.invalid/token")
	sender, err := NewFCMSender(saJSON, "explicit-project")
	if err != nil {
		t.Fatal(err)
	}
	if sender.ProjectID != "explicit-project" {
		t.Fatalf("unexpected ProjectID: %q", sender.ProjectID)
	}
}

func TestNewFCMSender_MissingProjectID(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sa := fcmServiceAccount{
		ClientEmail: "test@example.iam.gserviceaccount.com",
		PrivateKey:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		TokenURI:    "https://example.invalid/token",
	}
	b, _ := json.Marshal(sa)
	if _, err := NewFCMSender(b, ""); err == nil {
		t.Fatal("expected error for missing project id")
	}
}

func TestNewFCMSender_InvalidJSON(t *testing.T) {
	if _, err := NewFCMSender([]byte("not json"), "p"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// fcmRawPayload runs one Message through an FCMSender against a test server
// and returns the decoded "message" object the server received.
func fcmRawPayload(t *testing.T, m Message) map[string]any {
	t.Helper()
	var got struct {
		Message map[string]any `json:"message"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		_, _ = w.Write([]byte(`{"name":"projects/p/messages/1"}`))
	}))
	defer srv.Close()

	s := &FCMSender{ProjectID: "p", TokenSource: fakeTokenSource{accessToken: "secret"}, Endpoint: srv.URL}
	res, err := s.Send(context.Background(), []Message{m})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK || got.Message == nil {
		t.Fatalf("unexpected result %+v / payload %+v", res, got)
	}
	return got.Message
}

func TestFCMSender_Send_Options(t *testing.T) {
	ttl := 3600
	got := fcmRawPayload(t, Message{
		Platform: "android", DeviceToken: "device-1", Title: "t", Body: "b", ChannelID: "default", Sound: "default",
		TTLSeconds: &ttl, Priority: "normal", CollapseKey: "news", Image: "https://example.com/a.png",
		Subtitle: "ignored", ThreadID: "ignored", InterruptionLevel: "critical",
		Data: map[string]any{"k": "v"},
	})
	want := map[string]any{
		"token":        "device-1",
		"notification": map[string]any{"title": "t", "body": "b", "image": "https://example.com/a.png"},
		"data":         map[string]any{"k": "v"},
		"android": map[string]any{
			"priority": "normal", "ttl": "3600s", "collapse_key": "news",
			"notification": map[string]any{"channel_id": "default", "sound": "default"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected payload:\n got %v\nwant %v", got, want)
	}
}

func TestFCMSender_Send_Silent(t *testing.T) {
	ttl := 0
	got := fcmRawPayload(t, Message{
		Platform: "android", DeviceToken: "device-1", Silent: true, TTLSeconds: &ttl, CollapseKey: "sync",
		ChannelID: "default", Data: map[string]any{"k": "v"},
	})
	want := map[string]any{
		"token": "device-1",
		"data":  map[string]any{"k": "v"},
		"android": map[string]any{
			"priority": "high", "ttl": "0s", "collapse_key": "sync",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected payload:\n got %v\nwant %v", got, want)
	}
}

// TestFCMSender_Send_NoOptionsPayloadUnchanged pins the payload for a
// message without any of the option fields (it must not gain new keys).
func TestFCMSender_Send_NoOptionsPayloadUnchanged(t *testing.T) {
	got := fcmRawPayload(t, Message{DeviceToken: "device-1", Title: "t", Body: "b"})
	want := map[string]any{
		"token":        "device-1",
		"notification": map[string]any{"title": "t", "body": "b"},
		"android":      map[string]any{"priority": "high"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected payload:\n got %v\nwant %v", got, want)
	}
}

func TestFCMSender_Send_ExplicitHighPriority(t *testing.T) {
	got := fcmRawPayload(t, Message{DeviceToken: "device-1", Title: "t", Priority: "high"})
	android, _ := got["android"].(map[string]any)
	if android["priority"] != "high" {
		t.Fatalf("unexpected android config: %v", android)
	}
}

package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeTokenSource returns a fixed access token without any network call.
type fakeTokenSource struct {
	accessToken string
	err         error
}

func (f fakeTokenSource) Token() (*oauth2.Token, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &oauth2.Token{AccessToken: f.accessToken, Expiry: time.Now().Add(time.Hour)}, nil
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

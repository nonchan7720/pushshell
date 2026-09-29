package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestExpoSender_Send(t *testing.T) {
	var got []expoMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing access token header")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"status":"ok","id":"1"},
			{"status":"error","message":"not registered","details":{"error":"DeviceNotRegistered"}},
			{"status":"error","message":"too big","details":{"error":"MessageTooBig"}}
		]}`))
	}))
	defer srv.Close()

	s := &ExpoSender{Endpoint: srv.URL, AccessToken: "secret"}
	badge := 3
	res, err := s.Send(context.Background(), []Message{
		{ExpoToken: "a", Title: "t", Body: "b", Data: map[string]any{"url": "https://x"}, Badge: &badge, ChannelID: "default"},
		{ExpoToken: "b", Title: "t"},
		{ExpoToken: "c", Title: "t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].To != "a" || got[0].ChannelID != "default" || got[0].Badge == nil || *got[0].Badge != 3 {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if !res[0].OK || res[1].OK || res[2].OK {
		t.Fatalf("unexpected results: %+v", res)
	}
	if !res[1].Unregistered || res[2].Unregistered {
		t.Fatalf("unexpected unregistered flags: %+v", res)
	}
}

func TestExpoSender_Batches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var msgs []expoMessage
		_ = json.NewDecoder(r.Body).Decode(&msgs)
		tickets := make([]expoTicket, len(msgs))
		for i := range tickets {
			tickets[i] = expoTicket{Status: "ok"}
		}
		_ = json.NewEncoder(w).Encode(expoResponse{Data: tickets})
	}))
	defer srv.Close()

	msgs := make([]Message, 250)
	for i := range msgs {
		msgs[i] = Message{ExpoToken: "x"}
	}
	res, err := (&ExpoSender{Endpoint: srv.URL}).Send(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 250 || calls != 3 {
		t.Fatalf("got %d results in %d calls", len(res), calls)
	}
}

func TestExpoSender_NoToken(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var msgs []expoMessage
		_ = json.NewDecoder(r.Body).Decode(&msgs)
		tickets := make([]expoTicket, len(msgs))
		for i := range tickets {
			tickets[i] = expoTicket{Status: "ok"}
		}
		_ = json.NewEncoder(w).Encode(expoResponse{Data: tickets})
	}))
	defer srv.Close()

	s := &ExpoSender{Endpoint: srv.URL}
	res, err := s.Send(context.Background(), []Message{
		{ExpoToken: "a", Title: "t"},
		{Title: "t"}, // no ExpoToken
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("unexpected results: %+v", res)
	}
	if !res[0].OK {
		t.Fatalf("expected res[0] OK, got %+v", res[0])
	}
	if res[1].OK || res[1].Error != "device has no expo push token" {
		t.Fatalf("unexpected res[1]: %+v", res[1])
	}
	if !called {
		t.Fatal("expected server to be called for the one message with a token")
	}
}

// expoRawPayload runs one Message through an ExpoSender against a test
// server and returns the decoded JSON object the server received for it.
func expoRawPayload(t *testing.T, m Message) map[string]any {
	t.Helper()
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"1"}]}`))
	}))
	defer srv.Close()

	res, err := (&ExpoSender{Endpoint: srv.URL}).Send(context.Background(), []Message{m})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK || len(got) != 1 {
		t.Fatalf("unexpected result %+v / payload %+v", res, got)
	}
	return got[0]
}

func TestExpoSender_Send_Options(t *testing.T) {
	ttl := 3600
	badge := 2
	got := expoRawPayload(t, Message{
		ExpoToken: "a", Title: "t", Body: "b", Sound: "default", Badge: &badge,
		TTLSeconds: &ttl, Priority: "normal", CollapseKey: "ignored", Image: "https://example.com/a.png",
		Subtitle: "sub", ThreadID: "ignored", InterruptionLevel: "time-sensitive",
		Data: map[string]any{"k": "v"},
	})
	want := map[string]any{
		"to": "a", "title": "t", "body": "b", "sound": "default", "badge": float64(2),
		"data": map[string]any{"k": "v"},
		"ttl":  float64(3600), "priority": "normal",
		"richContent": map[string]any{"image": "https://example.com/a.png"},
		"subtitle":    "sub", "interruptionLevel": "time-sensitive",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected payload:\n got %v\nwant %v", got, want)
	}
}

func TestExpoSender_Send_Silent(t *testing.T) {
	ttl := 0
	got := expoRawPayload(t, Message{
		ExpoToken: "a", Silent: true, Priority: "high", TTLSeconds: &ttl,
		Data: map[string]any{"k": "v"},
	})
	want := map[string]any{
		"to": "a", "data": map[string]any{"k": "v"},
		"_contentAvailable": true, "priority": "high", "ttl": float64(0),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected payload:\n got %v\nwant %v", got, want)
	}
}

// TestExpoSender_Send_NoOptionsPayloadUnchanged pins the payload for a
// message without any of the option fields (it must not gain new keys).
func TestExpoSender_Send_NoOptionsPayloadUnchanged(t *testing.T) {
	got := expoRawPayload(t, Message{ExpoToken: "a", Title: "t", Body: "b", ChannelID: "default"})
	want := map[string]any{"to": "a", "title": "t", "body": "b", "channelId": "default"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected payload:\n got %v\nwant %v", got, want)
	}
}

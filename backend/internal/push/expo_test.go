package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		{To: "a", Title: "t", Body: "b", Data: map[string]any{"url": "https://x"}, Badge: &badge, ChannelID: "default"},
		{To: "b", Title: "t"},
		{To: "c", Title: "t"},
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
		msgs[i] = Message{To: "x"}
	}
	res, err := (&ExpoSender{Endpoint: srv.URL}).Send(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 250 || calls != 3 {
		t.Fatalf("got %d results in %d calls", len(res), calls)
	}
}

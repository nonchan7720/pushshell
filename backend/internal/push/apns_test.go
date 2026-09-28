package push

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sideshow/apns2"
)

// fakeAPNSClient implements apnsClient without any real network/HTTP2 call,
// so we can exercise APNSSender.Send end to end (dispatch, error mapping,
// ordering) without an actual APNs connection.
type fakeAPNSClient struct {
	// byToken maps a DeviceToken to the Response to return for it. Missing
	// entries get a generic 200 "sent" response.
	byToken map[string]*apns2.Response
	calls   []*apns2.Notification
}

func (f *fakeAPNSClient) PushWithContext(_ apns2.Context, n *apns2.Notification) (*apns2.Response, error) {
	f.calls = append(f.calls, n)
	if resp, ok := f.byToken[n.DeviceToken]; ok {
		return resp, nil
	}
	return &apns2.Response{StatusCode: http.StatusOK}, nil
}

func TestAPNSSender_Send_OK(t *testing.T) {
	fake := &fakeAPNSClient{}
	s := &APNSSender{Client: fake, Topic: "com.example.app"}
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
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	n := fake.calls[0]
	if n.DeviceToken != "abc123" || n.Topic != "com.example.app" {
		t.Fatalf("unexpected notification: %+v", n)
	}
	if n.Priority != apns2.PriorityHigh || n.PushType != apns2.PushTypeAlert {
		t.Fatalf("unexpected priority/pushtype: %+v", n)
	}
	raw, err := json.Marshal(n.Payload)
	if err != nil {
		t.Fatal(err)
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
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Aps.Alert.Title != "t" || decoded.Aps.Alert.Body != "b" || decoded.Aps.Sound != "default" || decoded.Aps.Badge != 5 {
		t.Fatalf("unexpected payload: %s", raw)
	}
	if decoded.URL != "https://x" {
		t.Fatalf("expected custom data key at top level: %s", raw)
	}
}

func TestAPNSSender_Send_NoDeviceToken(t *testing.T) {
	fake := &fakeAPNSClient{}
	s := &APNSSender{Client: fake, Topic: "com.example.app"}
	res, err := s.Send(context.Background(), []Message{{}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Error != "device has no native token" {
		t.Fatalf("unexpected result: %+v", res[0])
	}
	if len(fake.calls) != 0 {
		t.Fatal("must not call APNs without a device token")
	}
}

func TestClassifyAPNSResponse(t *testing.T) {
	cases := []struct {
		name         string
		resp         *apns2.Response
		wantOK       bool
		wantUnreg    bool
		wantErrEmpty bool
	}{
		{"sent", &apns2.Response{StatusCode: http.StatusOK}, true, false, true},
		{"bad device token", &apns2.Response{StatusCode: http.StatusBadRequest, Reason: apns2.ReasonBadDeviceToken}, false, true, false},
		{"unregistered", &apns2.Response{StatusCode: http.StatusGone, Reason: apns2.ReasonUnregistered}, false, true, false},
		{"expired token", &apns2.Response{StatusCode: http.StatusBadRequest, Reason: apns2.ReasonExpiredToken}, false, true, false},
		{"other error", &apns2.Response{StatusCode: http.StatusBadRequest, Reason: apns2.ReasonBadTopic}, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := classifyAPNSResponse(c.resp)
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
	fake := &fakeAPNSClient{
		byToken: map[string]*apns2.Response{
			"bad-3": {StatusCode: http.StatusBadRequest, Reason: apns2.ReasonBadDeviceToken},
		},
	}
	s := &APNSSender{Client: fake, Topic: "com.example.app", Concurrency: 3}
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

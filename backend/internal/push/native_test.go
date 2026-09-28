package push

import (
	"context"
	"testing"
)

// recordingSender records which messages it was asked to send (in the order
// given) and returns a canned result per call, tagged with the platform so
// tests can check which sub-sender actually handled each message.
type recordingSender struct {
	tag  string
	sent []Message
}

func (s *recordingSender) Send(_ context.Context, messages []Message) ([]Result, error) {
	s.sent = append(s.sent, messages...)
	results := make([]Result, len(messages))
	for i := range results {
		results[i] = Result{OK: true, Error: s.tag}
	}
	return results, nil
}

func TestNativeSender_SplitsByPlatformAndPreservesOrder(t *testing.T) {
	android := &recordingSender{tag: "android"}
	ios := &recordingSender{tag: "ios"}
	s := NativeSender{Android: android, IOS: ios}

	msgs := []Message{
		{Platform: "ios", DeviceToken: "i1"},
		{Platform: "android", DeviceToken: "a1"},
		{Platform: "ios", DeviceToken: "i2"},
		{Platform: "android", DeviceToken: "a2"},
	}
	res, err := s.Send(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 {
		t.Fatalf("unexpected result count: %d", len(res))
	}
	// Order preserved, and each result came from the right platform's sender.
	wantTags := []string{"ios", "android", "ios", "android"}
	for i, want := range wantTags {
		if res[i].Error != want {
			t.Fatalf("index %d: got tag %q want %q", i, res[i].Error, want)
		}
	}
	if len(android.sent) != 2 || android.sent[0].DeviceToken != "a1" || android.sent[1].DeviceToken != "a2" {
		t.Fatalf("unexpected android batch: %+v", android.sent)
	}
	if len(ios.sent) != 2 || ios.sent[0].DeviceToken != "i1" || ios.sent[1].DeviceToken != "i2" {
		t.Fatalf("unexpected ios batch: %+v", ios.sent)
	}
}

func TestNativeSender_MissingSubSender(t *testing.T) {
	s := NativeSender{Android: &recordingSender{tag: "android"}, IOS: nil}
	res, err := s.Send(context.Background(), []Message{
		{Platform: "ios", DeviceToken: "i1"},
		{Platform: "android", DeviceToken: "a1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Error != "no apns sender configured" {
		t.Fatalf("unexpected ios result: %+v", res[0])
	}
	if !res[1].OK {
		t.Fatalf("unexpected android result: %+v", res[1])
	}
}

func TestNativeSender_NoDeviceToken(t *testing.T) {
	s := NativeSender{Android: &recordingSender{tag: "android"}, IOS: &recordingSender{tag: "ios"}}
	res, err := s.Send(context.Background(), []Message{
		{Platform: "android"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Error != "device has no native token" {
		t.Fatalf("unexpected result: %+v", res[0])
	}
}

func TestNativeSender_UnsupportedPlatform(t *testing.T) {
	s := NativeSender{Android: &recordingSender{tag: "android"}, IOS: &recordingSender{tag: "ios"}}
	res, err := s.Send(context.Background(), []Message{
		{Platform: "windows", DeviceToken: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].OK || res[0].Error == "" {
		t.Fatalf("expected error for unsupported platform, got %+v", res[0])
	}
}

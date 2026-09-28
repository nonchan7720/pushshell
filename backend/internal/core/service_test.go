package core_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sort"
	"testing"

	"github.com/nonchan7720/webapp-notification/backend/internal/core"
	"github.com/nonchan7720/webapp-notification/backend/internal/push"
)

// fakeStore is an in-memory core.Store good enough to exercise Service: it
// applies the same "" -> "keep existing value" upsert semantics and
// cascade/idempotency rules internal/store/storetest checks against the
// real implementations.
type fakeStore struct {
	nextID  int64
	devices map[string]*core.Device    // installationID -> device (LoginIDs unused here)
	links   map[string]map[string]bool // installationID -> set of loginID
}

func newFakeStore() *fakeStore {
	return &fakeStore{devices: map[string]*core.Device{}, links: map[string]map[string]bool{}}
}

func (s *fakeStore) UpsertDevice(_ context.Context, in core.DeviceInput) (core.Device, error) {
	d, ok := s.devices[in.InstallationID]
	if !ok {
		s.nextID++
		d = &core.Device{ID: s.nextID, InstallationID: in.InstallationID}
		s.devices[in.InstallationID] = d
	}
	d.Platform = in.Platform
	if in.PushToken != "" {
		d.PushToken = in.PushToken
	}
	if in.DeviceToken != "" {
		d.DeviceToken = in.DeviceToken
	}
	if in.AppID != "" {
		d.AppID = in.AppID
	}
	if in.AppVersion != "" {
		d.AppVersion = in.AppVersion
	}
	if in.BuildNumber != "" {
		d.BuildNumber = in.BuildNumber
	}
	if in.OSVersion != "" {
		d.OSVersion = in.OSVersion
	}
	if in.DeviceModel != "" {
		d.DeviceModel = in.DeviceModel
	}
	if in.Locale != "" {
		d.Locale = in.Locale
	}
	cp := *d
	return cp, nil
}

func (s *fakeStore) LinkLogin(_ context.Context, installationID, loginID string) error {
	if _, ok := s.devices[installationID]; !ok {
		return core.ErrNotFound
	}
	set, ok := s.links[installationID]
	if !ok {
		set = map[string]bool{}
		s.links[installationID] = set
	}
	set[loginID] = true
	return nil
}

func (s *fakeStore) UnlinkLogin(_ context.Context, installationID, loginID string) error {
	if set, ok := s.links[installationID]; ok {
		delete(set, loginID)
	}
	return nil
}

func (s *fakeStore) UnlinkLoginEverywhere(_ context.Context, loginID string) (int, error) {
	n := 0
	for _, set := range s.links {
		if set[loginID] {
			delete(set, loginID)
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) DeleteDevice(_ context.Context, installationID string) error {
	delete(s.devices, installationID)
	delete(s.links, installationID)
	return nil
}

func (s *fakeStore) GetDevice(_ context.Context, installationID string) (core.Device, error) {
	d, ok := s.devices[installationID]
	if !ok {
		return core.Device{}, core.ErrNotFound
	}
	cp := *d
	cp.LoginIDs = s.sortedLogins(installationID)
	return cp, nil
}

func (s *fakeStore) FindDevicesByLogins(_ context.Context, loginIDs []string) ([]core.DeviceMatch, error) {
	want := map[string]bool{}
	for _, id := range loginIDs {
		want[id] = true
	}
	var installationIDs []string
	for installationID := range s.devices {
		installationIDs = append(installationIDs, installationID)
	}
	sort.Slice(installationIDs, func(i, j int) bool { return s.devices[installationIDs[i]].ID < s.devices[installationIDs[j]].ID })

	var out []core.DeviceMatch
	for _, installationID := range installationIDs {
		var matched []string
		for loginID := range s.links[installationID] {
			if want[loginID] {
				matched = append(matched, loginID)
			}
		}
		if len(matched) == 0 {
			continue
		}
		sort.Strings(matched)
		d := *s.devices[installationID]
		out = append(out, core.DeviceMatch{Device: d, LoginIDs: matched})
	}
	return out, nil
}

func (s *fakeStore) sortedLogins(installationID string) []string {
	set := s.links[installationID]
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

var _ core.Store = (*fakeStore)(nil)

// fakeSender records every batch sent and returns a scripted result per message.
type fakeSender struct {
	sent    [][]push.Message
	results func([]push.Message) []push.Result
}

func (f *fakeSender) Send(_ context.Context, msgs []push.Message) ([]push.Result, error) {
	f.sent = append(f.sent, msgs)
	if f.results != nil {
		return f.results(msgs), nil
	}
	out := make([]push.Result, len(msgs))
	for i := range out {
		out[i] = push.Result{OK: true}
	}
	return out, nil
}

func newTestService(sender push.Sender) (*core.Service, *fakeStore) {
	store := newFakeStore()
	logger := slog.New(slog.DiscardHandler)
	return core.New(store, sender, core.AllowAll{}, logger), store
}

func createDevice(t *testing.T, store *fakeStore, installationID, platform, pushToken string, loginIDs ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: installationID, Platform: platform, PushToken: pushToken,
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	for _, id := range loginIDs {
		if err := store.LinkLogin(ctx, installationID, id); err != nil {
			t.Fatalf("seed link: %v", err)
		}
	}
}

func sortedStrings(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestService_Register_UpsertAndGet(t *testing.T) {
	svc, store := newTestService(&fakeSender{})
	ctx := context.Background()

	d, err := svc.Register(ctx, core.DeviceInput{
		LoginID: "user-1", InstallationID: "inst-1", Platform: "ios",
		PushToken: "ExponentPushToken[aaa]", AppVersion: "1.0.0", DeviceModel: "iPhone",
	}, "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !reflect.DeepEqual(d.LoginIDs, []string{"user-1"}) || d.ID == 0 {
		t.Fatalf("unexpected device: %+v", d)
	}

	// Re-register the same installationId with a different loginId/token:
	// the device stays one row, and loginIds is added to (not replaced).
	d2, err := svc.Register(ctx, core.DeviceInput{
		LoginID: "user-2", InstallationID: "inst-1", Platform: "ios",
		PushToken: "ExponentPushToken[bbb]",
	}, "")
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if d2.ID != d.ID {
		t.Fatalf("expected same device id, got %d and %d", d.ID, d2.ID)
	}
	if !reflect.DeepEqual(sortedStrings(d2.LoginIDs), []string{"user-1", "user-2"}) {
		t.Fatalf("expected both loginIds, got %+v", d2.LoginIDs)
	}
	if d2.PushToken != "ExponentPushToken[bbb]" {
		t.Fatalf("push token not updated: %+v", d2)
	}
	if len(store.devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(store.devices))
	}

	// Registering again with the same (installationId, loginId) is idempotent.
	if _, err := svc.Register(ctx, core.DeviceInput{
		LoginID: "user-2", InstallationID: "inst-1", Platform: "ios", PushToken: "ExponentPushToken[bbb]",
	}, ""); err != nil {
		t.Fatalf("re-register (same login): %v", err)
	}
	if n := len(store.links["inst-1"]); n != 2 {
		t.Fatalf("expected still 2 links, got %d", n)
	}

	got, err := svc.GetDevice(ctx, "inst-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if !reflect.DeepEqual(sortedStrings(got.LoginIDs), []string{"user-1", "user-2"}) {
		t.Fatalf("unexpected loginIds: %+v", got.LoginIDs)
	}

	if _, err := svc.GetDevice(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Register_Validation(t *testing.T) {
	svc, _ := newTestService(&fakeSender{})
	ctx := context.Background()

	_, err := svc.Register(ctx, core.DeviceInput{
		LoginID: "u", InstallationID: "i", Platform: "windows", PushToken: "t",
	}, "")
	var inv core.ErrInvalidInput
	if !errors.As(err, &inv) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestService_Register_RequiresPushOrDeviceToken(t *testing.T) {
	svc, store := newTestService(&fakeSender{})
	ctx := context.Background()

	_, err := svc.Register(ctx, core.DeviceInput{
		LoginID: "u", InstallationID: "inst-none", Platform: "android",
	}, "")
	var inv core.ErrInvalidInput
	if !errors.As(err, &inv) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
	if len(store.devices) != 0 {
		t.Fatalf("expected no device created, got %d", len(store.devices))
	}

	d, err := svc.Register(ctx, core.DeviceInput{
		LoginID: "u", InstallationID: "inst-native", Platform: "android",
		DeviceToken: "fcm-registration-token",
	}, "")
	if err != nil {
		t.Fatalf("register (deviceToken only): %v", err)
	}
	if d.PushToken != "" {
		t.Fatalf("expected no pushToken, got %+v", d)
	}
	if d.DeviceToken != "fcm-registration-token" {
		t.Fatalf("unexpected deviceToken: %+v", d)
	}
}

func TestService_UnregisterDevice(t *testing.T) {
	svc, store := newTestService(&fakeSender{})
	ctx := context.Background()

	createDevice(t, store, "inst-1", "android", "t", "u1", "u2")

	if err := svc.UnregisterDevice(ctx, "inst-1", ""); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if len(store.devices) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(store.devices))
	}
	if len(store.links["inst-1"]) != 0 {
		t.Fatalf("expected links to cascade-delete")
	}
	// Not existing is not an error.
	if err := svc.UnregisterDevice(ctx, "inst-1", ""); err != nil {
		t.Fatalf("unregister (already gone): %v", err)
	}
}

func TestService_UnregisterDeviceLogin(t *testing.T) {
	svc, store := newTestService(&fakeSender{})
	ctx := context.Background()

	createDevice(t, store, "inst-1", "android", "t", "u1", "u2")

	if err := svc.UnregisterDeviceLogin(ctx, "inst-1", "u1", ""); err != nil {
		t.Fatalf("unregister device login: %v", err)
	}
	if len(store.devices) != 1 {
		t.Fatalf("expected device to remain")
	}
	got, err := svc.GetDevice(ctx, "inst-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if !reflect.DeepEqual(got.LoginIDs, []string{"u2"}) {
		t.Fatalf("unexpected loginIds after unlink: %+v", got.LoginIDs)
	}

	// Unlinking an absent link, or an absent device, is not an error.
	if err := svc.UnregisterDeviceLogin(ctx, "inst-1", "u1", ""); err != nil {
		t.Fatalf("unregister (already unlinked): %v", err)
	}
	if err := svc.UnregisterDeviceLogin(ctx, "nope", "u1", ""); err != nil {
		t.Fatalf("unregister (no device): %v", err)
	}
}

func TestService_UnregisterLogin(t *testing.T) {
	svc, store := newTestService(&fakeSender{})
	ctx := context.Background()

	createDevice(t, store, "inst-1", "android", "t1", "u1", "u2")
	createDevice(t, store, "inst-2", "ios", "t2", "u1")
	createDevice(t, store, "inst-3", "ios", "t3", "u3")

	n, err := svc.UnregisterLogin(ctx, "u1")
	if err != nil {
		t.Fatalf("unregister login: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 removed, got %d", n)
	}
	if len(store.devices) != 3 {
		t.Fatalf("expected devices to remain, got %d", len(store.devices))
	}

	n2, err := svc.UnregisterLogin(ctx, "u1")
	if err != nil {
		t.Fatalf("unregister login again: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 removed the second time, got %d", n2)
	}
}

func TestService_Send(t *testing.T) {
	sender := &fakeSender{
		results: func(msgs []push.Message) []push.Result {
			out := make([]push.Result, len(msgs))
			for i, m := range msgs {
				if m.ExpoToken == "dead" {
					out[i] = push.Result{Error: "DeviceNotRegistered", Unregistered: true}
				} else {
					out[i] = push.Result{OK: true}
				}
			}
			return out
		},
	}
	svc, store := newTestService(sender)
	ctx := context.Background()

	createDevice(t, store, "a", "ios", "tok-a", "u1")
	createDevice(t, store, "b", "android", "dead", "u1")
	createDevice(t, store, "c", "ios", "tok-c", "u2")
	createDevice(t, store, "d", "ios", "tok-d", "other")

	res, err := svc.Send(ctx, core.SendInput{
		LoginIDs: []string{"u1", "u2"}, Title: "hello", Body: "world",
		URL: "https://example.com/inbox", Data: map[string]any{"kind": "message"},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Requested != 3 || res.Sent != 2 || res.Failed != 1 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 3 {
		t.Fatalf("unexpected sent batches: %+v", sender.sent)
	}
	m := sender.sent[0][0]
	if m.Title != "hello" || m.Body != "world" || m.Data["url"] != "https://example.com/inbox" || m.Data["kind"] != "message" {
		t.Fatalf("unexpected message: %+v", m)
	}

	for _, dr := range res.Results {
		switch dr.InstallationID {
		case "a":
			if !reflect.DeepEqual(dr.LoginIDs, []string{"u1"}) {
				t.Fatalf("unexpected loginIds for a: %+v", dr)
			}
		case "b":
			if dr.Status != "error" || !dr.Unregistered {
				t.Fatalf("expected unregistered error for b: %+v", dr)
			}
		case "c":
			if !reflect.DeepEqual(dr.LoginIDs, []string{"u2"}) {
				t.Fatalf("unexpected loginIds for c: %+v", dr)
			}
		case "d":
			t.Fatalf("device d should not have been targeted: %+v", dr)
		}
	}
	if len(store.devices) != 3 {
		t.Fatalf("expected dead device deleted, got %d devices", len(store.devices))
	}

	none, err := svc.Send(ctx, core.SendInput{LoginIDs: []string{"nobody"}, Title: "x"})
	if err != nil {
		t.Fatalf("send (no targets): %v", err)
	}
	if none.Requested != 0 {
		t.Fatalf("expected empty result, got %+v", none)
	}
}

func TestService_Send_NativeDevice(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(sender)
	ctx := context.Background()

	if _, err := store.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: "native-1", Platform: "android",
		PushToken: "ExponentPushToken[aaa]", DeviceToken: "fcm-registration-token",
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := store.LinkLogin(ctx, "native-1", "u1"); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	res, err := svc.Send(ctx, core.SendInput{LoginIDs: []string{"u1"}, Title: "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Sent != 1 {
		t.Fatalf("send: %+v", res)
	}
	m := sender.sent[0][0]
	if m.Platform != "android" || m.ExpoToken != "ExponentPushToken[aaa]" || m.DeviceToken != "fcm-registration-token" {
		t.Fatalf("unexpected message: %+v", m)
	}
}

// TestService_Send_DedupesPerDevice checks that a device linked to several
// of the requested login IDs (many-to-many) still gets exactly one
// push.Message, with a single DeliveryResult listing every matched login ID.
func TestService_Send_DedupesPerDevice(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(sender)
	ctx := context.Background()

	createDevice(t, store, "shared", "ios", "tok", "u1", "u2")
	createDevice(t, store, "solo", "ios", "tok-solo", "u2")

	res, err := svc.Send(ctx, core.SendInput{LoginIDs: []string{"u1", "u2"}, Title: "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Requested != 2 || res.Sent != 2 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 2 {
		t.Fatalf("expected exactly one push per device (2 total), got: %+v", sender.sent)
	}
	var shared *core.DeliveryResult
	for i := range res.Results {
		if res.Results[i].InstallationID == "shared" {
			shared = &res.Results[i]
		}
	}
	if shared == nil {
		t.Fatalf("no result for shared device: %+v", res.Results)
	}
	if !reflect.DeepEqual(sortedStrings(shared.LoginIDs), []string{"u1", "u2"}) {
		t.Fatalf("expected both loginIds on the single result, got %+v", shared.LoginIDs)
	}
}

func TestService_Send_Validation(t *testing.T) {
	svc, _ := newTestService(&fakeSender{})
	ctx := context.Background()

	cases := []core.SendInput{
		{LoginIDs: nil, Title: "x"},
		{LoginIDs: []string{"u1"}, Title: ""},
		{LoginIDs: []string{"u1"}, Title: "x", URL: "not-a-url"},
	}
	for i, in := range cases {
		if _, err := svc.Send(ctx, in); !errors.As(err, new(core.ErrInvalidInput)) {
			t.Fatalf("case %d: expected ErrInvalidInput, got %v", i, err)
		}
	}
}

// authorizerFunc adapts a func to core.Authorizer.
type authorizerFunc func(ctx context.Context, loginID, token string) error

func (f authorizerFunc) AuthorizeDevice(ctx context.Context, loginID, token string) error {
	return f(ctx, loginID, token)
}

func TestService_Register_Unauthorized(t *testing.T) {
	store := newFakeStore()
	svc := core.New(store, &fakeSender{}, authorizerFunc(func(context.Context, string, string) error {
		return core.ErrUnauthorized
	}), slog.New(slog.DiscardHandler))

	_, err := svc.Register(context.Background(), core.DeviceInput{
		LoginID: "u", InstallationID: "i", Platform: "ios", PushToken: "t",
	}, "bad-token")
	if !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if len(store.devices) != 0 {
		t.Fatalf("expected no device created, got %d", len(store.devices))
	}
}

package core_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nonchan7720/pushshell/backend/internal/core"
	"github.com/nonchan7720/pushshell/backend/internal/push"
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

func (s *fakeStore) FindDevicesByInstallationIDs(_ context.Context, installationIDs []string) ([]core.Device, error) {
	want := map[string]bool{}
	for _, id := range installationIDs {
		want[id] = true
	}
	return s.listSorted(func(d *core.Device) bool { return want[d.InstallationID] }), nil
}

// ListDevices mimics the Store contract: a plain (not tag-boundary)
// case-insensitive locale prefix pre-filter, so the tests below also show
// Service.Send re-applying the exact rule.
func (s *fakeStore) ListDevices(_ context.Context, filter core.DeviceFilter) ([]core.Device, error) {
	return s.listSorted(func(d *core.Device) bool {
		if len(filter.Platforms) > 0 && !slices.Contains(filter.Platforms, d.Platform) {
			return false
		}
		if len(filter.LocalePrefixes) > 0 {
			ok := false
			for _, p := range filter.LocalePrefixes {
				if d.Locale != "" && strings.HasPrefix(strings.ToLower(d.Locale), strings.ToLower(p)) {
					ok = true
				}
			}
			if !ok {
				return false
			}
		}
		return true
	}), nil
}

func (s *fakeStore) listSorted(keep func(*core.Device) bool) []core.Device {
	var out []core.Device
	for installationID, d := range s.devices {
		if keep(d) {
			cp := *d
			cp.LoginIDs = s.sortedLogins(installationID)
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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
	createDeviceWithLocale(t, store, installationID, platform, pushToken, "", loginIDs...)
}

func createDeviceWithLocale(t *testing.T, store *fakeStore, installationID, platform, pushToken, locale string, loginIDs ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: installationID, Platform: platform, PushToken: pushToken, Locale: locale,
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
		{LoginIDs: []string{"u1", ""}, Title: "x"},
	}
	for i, in := range cases {
		if _, err := svc.Send(ctx, in); !errors.As(err, new(core.ErrInvalidInput)) {
			t.Fatalf("case %d: expected ErrInvalidInput, got %v", i, err)
		}
	}
}

// requireInvalid checks that Send rejects in with an ErrInvalidInput whose
// message contains want.
func requireInvalid(t *testing.T, svc *core.Service, in core.SendInput, want string) {
	t.Helper()
	_, err := svc.Send(context.Background(), in)
	var inv core.ErrInvalidInput
	if !errors.As(err, &inv) {
		t.Fatalf("expected ErrInvalidInput containing %q, got %v", want, err)
	}
	if !strings.Contains(inv.Message, want) {
		t.Fatalf("expected error containing %q, got %q", want, inv.Message)
	}
}

func TestService_Send_TargetValidation(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(sender)

	requireInvalid(t, svc, core.SendInput{Title: "x"}, "at least one of loginIds, installationIds or broadcast")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: false, Filter: core.DeviceFilter{Platforms: []string{"ios"}}},
		"at least one of loginIds, installationIds or broadcast")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, LoginIDs: []string{"u1"}}, "broadcast cannot be combined")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, InstallationIDs: []string{"a"}}, "broadcast cannot be combined")
	requireInvalid(t, svc, core.SendInput{Title: "x", InstallationIDs: []string{"a", ""}}, "installationIds must not contain empty strings")
	requireInvalid(t, svc, core.SendInput{Title: "x", InstallationIDs: []string{strings.Repeat("a", 129)}}, "installationIds items must be at most 128")
	requireInvalid(t, svc, core.SendInput{Title: "x", InstallationIDs: make([]string, 1001)}, "installationIds must have at most 1000")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, Filter: core.DeviceFilter{Platforms: []string{"windows"}}}, "filter.platforms")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, Filter: core.DeviceFilter{Platforms: []string{"ios", "android", "ios"}}}, "filter.platforms must have 1 to 2 items")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, Filter: core.DeviceFilter{Platforms: []string{}}}, "filter.platforms must have 1 to 2 items")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, Filter: core.DeviceFilter{LocalePrefixes: []string{"ja", ""}}}, "filter.locales must not contain empty strings")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, Filter: core.DeviceFilter{LocalePrefixes: []string{strings.Repeat("a", 33)}}}, "filter.locales items must be at most 32")
	requireInvalid(t, svc, core.SendInput{Title: "x", Broadcast: true, Filter: core.DeviceFilter{LocalePrefixes: make([]string, 51)}}, "filter.locales must have 1 to 50 items")
	requireInvalid(t, svc, core.SendInput{Title: "", Broadcast: true}, "title is required")

	if len(sender.sent) != 0 {
		t.Fatalf("nothing should have been sent, got %+v", sender.sent)
	}
}

func TestService_Send_InstallationIDs(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(sender)
	ctx := context.Background()

	createDevice(t, store, "a", "ios", "tok-a", "u1")
	createDevice(t, store, "b", "android", "tok-b")
	createDevice(t, store, "c", "ios", "tok-c", "u2")

	res, err := svc.Send(ctx, core.SendInput{InstallationIDs: []string{"c", "unknown", "b"}, Title: "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Requested != 2 || res.Sent != 2 || res.Failed != 0 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	// Ordered by device ID ascending, not by request order.
	if got := []string{res.Results[0].InstallationID, res.Results[1].InstallationID}; !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("unexpected order: %v", got)
	}
	for _, dr := range res.Results {
		if len(dr.LoginIDs) != 0 {
			t.Fatalf("installationIds-only targets must not report loginIds: %+v", dr)
		}
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 2 || sender.sent[0][0].ExpoToken != "tok-b" || sender.sent[0][1].ExpoToken != "tok-c" {
		t.Fatalf("unexpected messages: %+v", sender.sent)
	}
}

// TestService_Send_LoginIDsAndInstallationIDsUnion checks that combining both
// targets yields the union with one message per device, and that
// DeliveryResult.LoginIDs only lists login IDs from the loginIds path.
func TestService_Send_LoginIDsAndInstallationIDsUnion(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(sender)
	ctx := context.Background()

	createDevice(t, store, "shared", "ios", "tok-shared", "u1", "u2") // via loginIds AND installationIds
	createDevice(t, store, "by-login", "ios", "tok-login", "u2")
	createDevice(t, store, "by-inst", "android", "tok-inst", "someone-else") // logins not requested
	createDevice(t, store, "unrelated", "android", "tok-unrelated", "u3")

	res, err := svc.Send(ctx, core.SendInput{
		LoginIDs:        []string{"u1", "u2"},
		InstallationIDs: []string{"by-inst", "shared"},
		Title:           "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Requested != 3 || res.Sent != 3 {
		t.Fatalf("unexpected counts: %+v", res)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 3 {
		t.Fatalf("expected exactly one message per device (3 total), got %+v", sender.sent)
	}
	want := []core.DeliveryResult{
		{InstallationID: "shared", LoginIDs: []string{"u1", "u2"}, Status: "ok"},
		{InstallationID: "by-login", LoginIDs: []string{"u2"}, Status: "ok"},
		{InstallationID: "by-inst", Status: "ok"},
	}
	if len(res.Results) != len(want) {
		t.Fatalf("unexpected results: %+v", res.Results)
	}
	for i, w := range want {
		g := res.Results[i]
		g.LoginIDs = sortedStrings(g.LoginIDs)
		if g.InstallationID != w.InstallationID || g.Status != w.Status || !slices.Equal(g.LoginIDs, w.LoginIDs) {
			t.Fatalf("result %d: got %+v want %+v", i, g, w)
		}
	}
}

func TestService_Send_BroadcastFilterPlatform(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(sender)
	ctx := context.Background()

	createDevice(t, store, "i1", "ios", "tok-i1", "u1")
	createDevice(t, store, "a1", "android", "tok-a1")
	createDevice(t, store, "i2", "ios", "tok-i2")

	all, err := svc.Send(ctx, core.SendInput{Broadcast: true, Title: "hello"})
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if all.Requested != 3 || all.Sent != 3 {
		t.Fatalf("unexpected broadcast counts: %+v", all)
	}
	// Broadcast reaches devices that have no login at all; LoginIDs stays
	// empty even for a device that is linked to a login.
	for _, dr := range all.Results {
		if len(dr.LoginIDs) != 0 {
			t.Fatalf("broadcast results must not report loginIds: %+v", dr)
		}
	}

	sender.sent = nil
	ios, err := svc.Send(ctx, core.SendInput{
		Broadcast: true, Title: "hello", Filter: core.DeviceFilter{Platforms: []string{"ios"}},
	})
	if err != nil {
		t.Fatalf("broadcast ios: %v", err)
	}
	if ios.Requested != 2 || ios.Results[0].InstallationID != "i1" || ios.Results[1].InstallationID != "i2" {
		t.Fatalf("unexpected ios results: %+v", ios)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 2 {
		t.Fatalf("unexpected messages: %+v", sender.sent)
	}

	none, err := svc.Send(ctx, core.SendInput{
		Broadcast: true, Title: "hello", Filter: core.DeviceFilter{Platforms: []string{"android"}, LocalePrefixes: []string{"ja"}},
	})
	if err != nil {
		t.Fatalf("broadcast none: %v", err)
	}
	if none.Requested != 0 || len(none.Results) != 0 {
		t.Fatalf("expected no targets, got %+v", none)
	}
}

func TestService_Send_BroadcastFilterLocale(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(sender)
	ctx := context.Background()

	createDeviceWithLocale(t, store, "ja", "ios", "t1", "ja")
	createDeviceWithLocale(t, store, "ja-jp", "ios", "t2", "ja-JP")
	createDeviceWithLocale(t, store, "jav", "ios", "t3", "jav") // Javanese: must not match "ja"
	createDeviceWithLocale(t, store, "en-us", "android", "t4", "en-US")
	createDeviceWithLocale(t, store, "ja-jp-x", "android", "t5", "ja-JP-u-ca-japanese")
	createDeviceWithLocale(t, store, "none", "android", "t6", "")

	sendTo := func(prefixes ...string) []string {
		t.Helper()
		res, err := svc.Send(ctx, core.SendInput{
			Broadcast: true, Title: "hello", Filter: core.DeviceFilter{LocalePrefixes: prefixes},
		})
		if err != nil {
			t.Fatalf("send %v: %v", prefixes, err)
		}
		var ids []string
		for _, dr := range res.Results {
			ids = append(ids, dr.InstallationID)
		}
		return ids
	}

	if got := sendTo("ja"); !slices.Equal(got, []string{"ja", "ja-jp", "ja-jp-x"}) {
		t.Fatalf("prefix ja: got %v", got)
	}
	if got := sendTo("JA-jp"); !slices.Equal(got, []string{"ja-jp", "ja-jp-x"}) {
		t.Fatalf("prefix ja-JP (case-insensitive): got %v", got)
	}
	if got := sendTo("ja-JP-u-ca-japanese"); !slices.Equal(got, []string{"ja-jp-x"}) {
		t.Fatalf("full tag: got %v", got)
	}
	if got := sendTo("jav"); !slices.Equal(got, []string{"jav"}) {
		t.Fatalf("prefix jav: got %v", got)
	}
	if got := sendTo("en", "jav"); !slices.Equal(got, []string{"jav", "en-us"}) {
		t.Fatalf("prefixes en,jav: got %v", got)
	}
	if got := sendTo("j"); len(got) != 0 {
		t.Fatalf("prefix j is not a tag prefix, got %v", got)
	}

	// The filter is also applied to loginIds / installationIds targets, and
	// a device with an empty locale never matches a locale filter.
	res, err := svc.Send(ctx, core.SendInput{
		InstallationIDs: []string{"ja", "jav", "none"}, Title: "hello",
		Filter: core.DeviceFilter{LocalePrefixes: []string{"ja"}},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Requested != 1 || res.Results[0].InstallationID != "ja" {
		t.Fatalf("unexpected filtered installationIds result: %+v", res)
	}
}

func TestMatchesFilter(t *testing.T) {
	d := func(platform, locale string) core.Device { return core.Device{Platform: platform, Locale: locale} }
	cases := []struct {
		name string
		dev  core.Device
		f    core.DeviceFilter
		want bool
	}{
		{"empty filter matches anything", d("ios", ""), core.DeviceFilter{}, true},
		{"platform match", d("ios", ""), core.DeviceFilter{Platforms: []string{"ios"}}, true},
		{"platform mismatch", d("ios", ""), core.DeviceFilter{Platforms: []string{"android"}}, false},
		{"either platform", d("ios", ""), core.DeviceFilter{Platforms: []string{"android", "ios"}}, true},
		{"empty locale", d("ios", ""), core.DeviceFilter{LocalePrefixes: []string{"ja"}}, false},
		{"exact", d("ios", "ja"), core.DeviceFilter{LocalePrefixes: []string{"ja"}}, true},
		{"region", d("ios", "ja-JP"), core.DeviceFilter{LocalePrefixes: []string{"ja"}}, true},
		{"not a tag boundary", d("ios", "jav"), core.DeviceFilter{LocalePrefixes: []string{"ja"}}, false},
		{"region prefix vs bare language", d("ios", "ja"), core.DeviceFilter{LocalePrefixes: []string{"ja-JP"}}, false},
		{"case insensitive", d("ios", "JA-jp"), core.DeviceFilter{LocalePrefixes: []string{"ja-JP"}}, true},
		{"platform and locale both required", d("android", "ja-JP"), core.DeviceFilter{Platforms: []string{"ios"}, LocalePrefixes: []string{"ja"}}, false},
	}
	for _, c := range cases {
		if got := core.MatchesFilter(c.dev, c.f); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestService_Send_SilentValidation(t *testing.T) {
	sender := &fakeSender{}
	svc, _ := newTestService(sender)
	const msg = "silent notifications carry data only"

	base := core.SendInput{LoginIDs: []string{"u1"}, Silent: true}
	badge := 1
	for name, in := range map[string]core.SendInput{
		"title":    {LoginIDs: base.LoginIDs, Silent: true, Title: "t"},
		"body":     {LoginIDs: base.LoginIDs, Silent: true, Body: "b"},
		"sound":    {LoginIDs: base.LoginIDs, Silent: true, Sound: "default"},
		"badge":    {LoginIDs: base.LoginIDs, Silent: true, Badge: &badge},
		"subtitle": {LoginIDs: base.LoginIDs, Silent: true, Subtitle: "s"},
		"image":    {LoginIDs: base.LoginIDs, Silent: true, Image: "https://example.com/a.png"},
	} {
		t.Run(name, func(t *testing.T) { requireInvalid(t, svc, in, msg) })
	}

	// A silent push needs no title, and data may be empty.
	if _, err := svc.Send(context.Background(), base); err != nil {
		t.Fatalf("silent without title/data: %v", err)
	}
	// Without silent, the title is still required.
	requireInvalid(t, svc, core.SendInput{LoginIDs: []string{"u1"}}, "title is required")
}

func TestService_Send_OptionValidation(t *testing.T) {
	svc, _ := newTestService(&fakeSender{})
	base := func(mod func(*core.SendInput)) core.SendInput {
		in := core.SendInput{LoginIDs: []string{"u1"}, Title: "x"}
		mod(&in)
		return in
	}
	intp := func(v int) *int { return &v }

	requireInvalid(t, svc, base(func(in *core.SendInput) { in.TTLSeconds = intp(-1) }), "ttl must be between 0 and 2419200")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.TTLSeconds = intp(2419201) }), "ttl must be between 0 and 2419200")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.Priority = "urgent" }), "priority must be")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.CollapseKey = strings.Repeat("k", 65) }), "collapseKey must be at most 64")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.Image = "/relative.png" }), "image must be an absolute http(s) URL")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.Image = "ftp://example.com/a.png" }), "image must be an absolute http(s) URL")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.Image = "https:///nohost.png" }), "image must be an absolute http(s) URL")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.Image = "https://example.com/" + strings.Repeat("a", 2048) }), "image must be at most 2048")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.Subtitle = strings.Repeat("s", 257) }), "subtitle must be at most 256")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.ThreadID = strings.Repeat("t", 65) }), "threadId must be at most 64")
	requireInvalid(t, svc, base(func(in *core.SendInput) { in.InterruptionLevel = "loud" }), "interruptionLevel must be one of")

	// Boundary values are accepted.
	for name, mod := range map[string]func(*core.SendInput){
		"ttl 0":          func(in *core.SendInput) { in.TTLSeconds = intp(0) },
		"ttl max":        func(in *core.SendInput) { in.TTLSeconds = intp(2419200) },
		"priority":       func(in *core.SendInput) { in.Priority = "normal" },
		"collapseKey":    func(in *core.SendInput) { in.CollapseKey = strings.Repeat("k", 64) },
		"image":          func(in *core.SendInput) { in.Image = "http://example.com/a.png?x=1" },
		"subtitle":       func(in *core.SendInput) { in.Subtitle = strings.Repeat("s", 256) },
		"threadId":       func(in *core.SendInput) { in.ThreadID = strings.Repeat("t", 64) },
		"passive":        func(in *core.SendInput) { in.InterruptionLevel = "passive" },
		"time-sensitive": func(in *core.SendInput) { in.InterruptionLevel = "time-sensitive" },
	} {
		if _, err := svc.Send(context.Background(), base(mod)); err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}

func TestService_Send_OptionsReachMessage(t *testing.T) {
	sender := &fakeSender{}
	svc, store := newTestService(sender)
	createDevice(t, store, "a", "ios", "tok-a", "u1")

	ttl := 3600
	_, err := svc.Send(context.Background(), core.SendInput{
		LoginIDs: []string{"u1"}, Title: "hello",
		TTLSeconds: &ttl, Priority: "normal", CollapseKey: "news", Image: "https://example.com/a.png",
		Subtitle: "sub", ThreadID: "thread-1", InterruptionLevel: "time-sensitive",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	m := sender.sent[0][0]
	if m.TTLSeconds == nil || *m.TTLSeconds != 3600 || m.Priority != "normal" || m.CollapseKey != "news" ||
		m.Image != "https://example.com/a.png" || m.Silent || m.Subtitle != "sub" ||
		m.ThreadID != "thread-1" || m.InterruptionLevel != "time-sensitive" {
		t.Fatalf("options did not reach the message: %+v", m)
	}

	sender.sent = nil
	_, err = svc.Send(context.Background(), core.SendInput{
		LoginIDs: []string{"u1"}, Silent: true, URL: "https://example.com/x", Data: map[string]any{"k": "v"},
	})
	if err != nil {
		t.Fatalf("send silent: %v", err)
	}
	m = sender.sent[0][0]
	if !m.Silent || m.Title != "" || m.Data["k"] != "v" || m.Data["url"] != "https://example.com/x" {
		t.Fatalf("unexpected silent message: %+v", m)
	}
	if m.TTLSeconds != nil || m.Priority != "" {
		t.Fatalf("unset options must stay unset: %+v", m)
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

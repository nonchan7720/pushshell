package httpapi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nonchan7720/pushshell/backend/internal/api"
	"github.com/nonchan7720/pushshell/backend/internal/core"
	"github.com/nonchan7720/pushshell/backend/internal/push"
	"github.com/nonchan7720/pushshell/backend/internal/transport/httpapi"
	"github.com/nonchan7720/pushshell/backend/internal/transport/httpapi/openapivalidate"
)

const testAPIKey = "test-api-key"

// fakeStore is a minimal in-memory core.Store, enough to exercise the HTTP
// layer end to end (see internal/core/service_test.go for the fuller
// version used to test Service's own logic).
type fakeStore struct {
	nextID  int64
	devices map[string]*core.Device
	links   map[string]map[string]bool
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
	set := s.links[installationID]
	cp.LoginIDs = make([]string, 0, len(set))
	for id := range set {
		cp.LoginIDs = append(cp.LoginIDs, id)
	}
	sort.Strings(cp.LoginIDs)
	return cp, nil
}

func (s *fakeStore) FindDevicesByLogins(_ context.Context, loginIDs []string) ([]core.DeviceMatch, error) {
	want := map[string]bool{}
	for _, id := range loginIDs {
		want[id] = true
	}
	var installationIDs []string
	for id := range s.devices {
		installationIDs = append(installationIDs, id)
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
		out = append(out, core.DeviceMatch{Device: *s.devices[installationID], LoginIDs: matched})
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

func (s *fakeStore) ListDevices(_ context.Context, filter core.DeviceFilter) ([]core.Device, error) {
	return s.listSorted(func(d *core.Device) bool {
		return len(filter.Platforms) == 0 || slices.Contains(filter.Platforms, d.Platform)
	}), nil
}

func (s *fakeStore) listSorted(keep func(*core.Device) bool) []core.Device {
	var out []core.Device
	for installationID, d := range s.devices {
		if !keep(d) {
			continue
		}
		cp := *d
		for loginID := range s.links[installationID] {
			cp.LoginIDs = append(cp.LoginIDs, loginID)
		}
		sort.Strings(cp.LoginIDs)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

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

func createDevice(t *testing.T, store *fakeStore, installationID, platform, pushToken string, loginIDs ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.UpsertDevice(ctx, core.DeviceInput{InstallationID: installationID, Platform: platform, PushToken: pushToken}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	for _, id := range loginIDs {
		if err := store.LinkLogin(ctx, installationID, id); err != nil {
			t.Fatalf("seed link: %v", err)
		}
	}
}

// newTestServer builds a full httpapi handler. withValidator selects
// between the two shapes this package is actually used in: cmd/server
// (with openapivalidate.Middleware) and cmd/worker (without it, only
// internal/core's own validation applies).
func newTestServer(t *testing.T, sender push.Sender, withValidator bool) (*httptest.Server, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	svc := core.New(store, sender, core.AllowAll{}, logger)

	opts := httpapi.Options{Service: svc, APIKey: testAPIKey, Logger: logger}
	if withValidator {
		v, err := openapivalidate.Middleware(testAPIKey)
		if err != nil {
			t.Fatalf("openapivalidate.Middleware: %v", err)
		}
		opts.Validator = v
	}
	h, err := httpapi.New(opts)
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, store
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func apiKeyEditor(_ context.Context, req *http.Request) error {
	req.Header.Set("X-API-Key", testAPIKey)
	return nil
}

func newClient(t *testing.T, srv *httptest.Server) *api.ClientWithResponses {
	t.Helper()
	c, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}
	return c
}

func ptr[T any](v T) *T { return &v }

func sortedStrings(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestHealthz(t *testing.T) {
	for _, withValidator := range []bool{true, false} {
		srv, _ := newTestServer(t, &fakeSender{}, withValidator)
		c := newClient(t, srv)
		res, err := c.HealthzWithResponse(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode() != http.StatusOK || res.JSON200 == nil || res.JSON200.Status != "ok" {
			t.Fatalf("withValidator=%v: unexpected response: %d %s", withValidator, res.StatusCode(), res.Body)
		}
	}
}

func TestRegisterDevice_UpsertAndGet(t *testing.T) {
	srv, _ := newTestServer(t, &fakeSender{}, true)
	c := newClient(t, srv)
	ctx := context.Background()

	res, err := c.RegisterDeviceWithResponse(ctx, api.DeviceRegistration{
		LoginId: "user-1", InstallationId: "inst-1", Platform: api.Ios, PushToken: ptr("ExponentPushToken[aaa]"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		t.Fatalf("register: %d %s", res.StatusCode(), res.Body)
	}
	if !reflect.DeepEqual(res.JSON200.LoginIds, []string{"user-1"}) || res.JSON200.Id == 0 {
		t.Fatalf("unexpected device: %+v", res.JSON200)
	}

	get, err := c.GetDeviceWithResponse(ctx, "inst-1")
	if err != nil {
		t.Fatal(err)
	}
	if get.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("expected 401 without api key, got %d", get.StatusCode())
	}
	get, err = c.GetDeviceWithResponse(ctx, "inst-1", apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if get.StatusCode() != http.StatusOK || get.JSON200 == nil || get.JSON200.Id != res.JSON200.Id {
		t.Fatalf("get: %d %s", get.StatusCode(), get.Body)
	}
	missing, err := c.GetDeviceWithResponse(ctx, "nope", apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", missing.StatusCode())
	}
}

// TestRegisterDevice_Validation_NoValidator checks that internal/core's own
// validation (not just the optional kin-openapi one) rejects a bad request
// — this is the path cmd/worker relies on (no Validator wired in).
func TestRegisterDevice_Validation_NoValidator(t *testing.T) {
	srv, store := newTestServer(t, &fakeSender{}, false)

	body := `{"loginId":"u","installationId":"i","platform":"windows","pushToken":"t"}`
	resp, err := http.Post(srv.URL+"/v1/devices", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	var e api.Error
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatal(err)
	}
	if e.Code != "invalid_request" {
		t.Fatalf("unexpected error body: %+v", e)
	}
	if len(store.devices) != 0 {
		t.Fatalf("expected no device created, got %d", len(store.devices))
	}
}

func TestUnregisterDeviceLogin(t *testing.T) {
	srv, store := newTestServer(t, &fakeSender{}, true)
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, store, "inst-1", "android", "t", "u1", "u2")

	res, err := c.UnregisterDeviceLoginWithResponse(ctx, "inst-1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("expected 204, got %d %s", res.StatusCode(), res.Body)
	}
	get, err := c.GetDeviceWithResponse(ctx, "inst-1", apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if get.StatusCode() != http.StatusOK || !reflect.DeepEqual(get.JSON200.LoginIds, []string{"u2"}) {
		t.Fatalf("unexpected loginIds after unlink: %d %+v", get.StatusCode(), get.JSON200)
	}
}

func TestUnregisterLogin(t *testing.T) {
	srv, store := newTestServer(t, &fakeSender{}, true)
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, store, "inst-1", "android", "t1", "u1", "u2")
	createDevice(t, store, "inst-2", "ios", "t2", "u1")

	unauth, err := c.UnregisterLoginWithResponse(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if unauth.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauth.StatusCode())
	}

	res, err := c.UnregisterLoginWithResponse(ctx, "u1", apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil || res.JSON200.Removed != 2 {
		t.Fatalf("unexpected result: %d %+v", res.StatusCode(), res.JSON200)
	}
}

func TestSendNotification_DedupesPerDevice(t *testing.T) {
	sender := &fakeSender{}
	srv, store := newTestServer(t, sender, true)
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, store, "shared", "ios", "tok", "u1", "u2")
	createDevice(t, store, "solo", "ios", "tok-solo", "u2")

	req := api.SendNotificationRequest{LoginIds: ptr([]string{"u1", "u2"}), Title: ptr("hello")}

	unauth, err := c.SendNotificationWithResponse(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if unauth.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauth.StatusCode())
	}
	if len(sender.sent) != 0 {
		t.Fatal("must not send without api key")
	}

	res, err := c.SendNotificationWithResponse(ctx, req, apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		t.Fatalf("send: %d %s", res.StatusCode(), res.Body)
	}
	r := res.JSON200
	if r.Requested != 2 || r.Sent != 2 {
		t.Fatalf("unexpected counts: %+v", r)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 2 {
		t.Fatalf("expected exactly one push per device (2 total), got: %+v", sender.sent)
	}
	var shared *api.DeliveryResult
	for i := range r.Results {
		if r.Results[i].InstallationId == "shared" {
			shared = &r.Results[i]
		}
	}
	if shared == nil {
		t.Fatalf("no result for shared device: %+v", r.Results)
	}
	if !reflect.DeepEqual(sortedStrings(shared.LoginIds), []string{"u1", "u2"}) {
		t.Fatalf("expected both loginIds on the single result, got %+v", shared.LoginIds)
	}
}

func TestSendNotification_UnregisteredDeviceDeleted(t *testing.T) {
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
	srv, store := newTestServer(t, sender, true)
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, store, "b", "android", "dead", "u1")

	res, err := c.SendNotificationWithResponse(ctx, api.SendNotificationRequest{LoginIds: ptr([]string{"u1"}), Title: ptr("x")}, apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil || res.JSON200.Failed != 1 {
		t.Fatalf("send: %d %s", res.StatusCode(), res.Body)
	}
	if len(store.devices) != 0 {
		t.Fatalf("expected unregistered device to be deleted, got %d devices", len(store.devices))
	}
}

func TestSendNotification_InstallationIDs(t *testing.T) {
	sender := &fakeSender{}
	srv, store := newTestServer(t, sender, true)
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, store, "a", "ios", "tok-a", "u1")
	createDevice(t, store, "b", "android", "tok-b")

	res, err := c.SendNotificationWithResponse(ctx, api.SendNotificationRequest{
		InstallationIds: ptr([]string{"b", "unknown"}), Title: ptr("hello"),
		Ttl: ptr(60), Priority: ptr(api.Normal),
	}, apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		t.Fatalf("send: %d %s", res.StatusCode(), res.Body)
	}
	r := res.JSON200
	if r.Requested != 1 || r.Sent != 1 || r.Failed != 0 || len(r.Results) != 1 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if r.Results[0].InstallationId != "b" || r.Results[0].LoginIds == nil || len(r.Results[0].LoginIds) != 0 {
		t.Fatalf("expected loginIds [] for an installationIds target: %+v", r.Results[0])
	}
	if !strings.Contains(string(res.Body), `"loginIds":[]`) {
		t.Fatalf("loginIds must be serialized as an empty array: %s", res.Body)
	}
	m := sender.sent[0][0]
	if m.ExpoToken != "tok-b" || m.TTLSeconds == nil || *m.TTLSeconds != 60 || m.Priority != "normal" {
		t.Fatalf("unexpected message: %+v", m)
	}
}

func TestSendNotification_BroadcastWithFilter(t *testing.T) {
	sender := &fakeSender{}
	srv, store := newTestServer(t, sender, true)
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, store, "i1", "ios", "tok-i1", "u1")
	createDevice(t, store, "a1", "android", "tok-a1")
	createDevice(t, store, "i2", "ios", "tok-i2")

	res, err := c.SendNotificationWithResponse(ctx, api.SendNotificationRequest{
		Broadcast: ptr(true), Title: ptr("hello"),
		Filter: &api.NotificationFilter{Platforms: ptr([]api.Platform{api.Ios}), Locales: ptr([]string{"ja"})},
	}, apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		t.Fatalf("send: %d %s", res.StatusCode(), res.Body)
	}
	// The fake devices have no locale, so the locale filter excludes them all.
	if res.JSON200.Requested != 0 || res.JSON200.Sent != 0 || len(res.JSON200.Results) != 0 {
		t.Fatalf("unexpected result: %+v", res.JSON200)
	}

	sender.sent = nil
	res, err = c.SendNotificationWithResponse(ctx, api.SendNotificationRequest{
		Broadcast: ptr(true), Title: ptr("hello"),
		Filter: &api.NotificationFilter{Platforms: ptr([]api.Platform{api.Ios})},
	}, apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		t.Fatalf("send: %d %s", res.StatusCode(), res.Body)
	}
	r := res.JSON200
	if r.Requested != 2 || r.Sent != 2 || len(r.Results) != 2 {
		t.Fatalf("unexpected result: %+v", r)
	}
	for _, dr := range r.Results {
		if dr.LoginIds == nil || len(dr.LoginIds) != 0 {
			t.Fatalf("expected loginIds [] for a broadcast target: %+v", dr)
		}
	}
	if r.Results[0].InstallationId != "i1" || r.Results[1].InstallationId != "i2" {
		t.Fatalf("unexpected order: %+v", r.Results)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 2 {
		t.Fatalf("unexpected messages: %+v", sender.sent)
	}
}

func TestSendNotification_BroadcastCombinedWithLoginIDsRejected(t *testing.T) {
	// Both with the OpenAPI validator (cmd/server) and without it (cmd/worker,
	// only core's own validation), the request is a 400 and nothing is sent.
	for _, withValidator := range []bool{true, false} {
		sender := &fakeSender{}
		srv, store := newTestServer(t, sender, withValidator)
		c := newClient(t, srv)
		createDevice(t, store, "a", "ios", "tok-a", "u1")

		res, err := c.SendNotificationWithResponse(context.Background(), api.SendNotificationRequest{
			Broadcast: ptr(true), LoginIds: ptr([]string{"u1"}), Title: ptr("hello"),
		}, apiKeyEditor)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode() != http.StatusBadRequest {
			t.Fatalf("validator=%v: expected 400, got %d %s", withValidator, res.StatusCode(), res.Body)
		}
		if !strings.Contains(string(res.Body), "broadcast cannot be combined") {
			t.Fatalf("validator=%v: unexpected error body: %s", withValidator, res.Body)
		}
		if len(sender.sent) != 0 {
			t.Fatalf("validator=%v: must not send", withValidator)
		}
	}
}

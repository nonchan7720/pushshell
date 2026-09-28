package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/nonchan7720/webapp-notification/backend/internal/api"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent/enttest"
	"github.com/nonchan7720/webapp-notification/backend/internal/handler"
	"github.com/nonchan7720/webapp-notification/backend/internal/push"
	"github.com/nonchan7720/webapp-notification/backend/internal/server"
)

const testAPIKey = "test-api-key"

// fakeSender は送信内容を記録し、あらかじめ決めた結果を返す。
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

func newTestServer(t *testing.T, sender push.Sender) (*httptest.Server, *ent.Client) {
	t.Helper()
	// modernc.org/sqlite は "sqlite" として登録されるので、database/sql で開いて ent の SQLite 方言に載せる。
	sqlDB, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	client := enttest.NewClient(t, enttest.WithOptions(ent.Driver(entsql.OpenDB(dialect.SQLite, sqlDB))))
	t.Cleanup(func() { _ = client.Close() })

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	h := handler.New(client, sender, handler.AllowAll{}, logger)
	mux, err := server.New(server.Options{Handler: h, APIKey: testAPIKey, Logger: logger})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, client
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

func TestHealthz(t *testing.T) {
	srv, _ := newTestServer(t, &fakeSender{})
	c := newClient(t, srv)

	res, err := c.HealthzWithResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil || res.JSON200.Status != "ok" {
		t.Fatalf("unexpected response: %d %s", res.StatusCode(), res.Body)
	}
}

func TestRegisterDevice_UpsertAndGet(t *testing.T) {
	srv, client := newTestServer(t, &fakeSender{})
	c := newClient(t, srv)
	ctx := context.Background()

	reg := api.DeviceRegistration{
		LoginId:        "user-1",
		InstallationId: "inst-1",
		Platform:       api.Ios,
		PushToken:      "ExponentPushToken[aaa]",
		AppVersion:     ptr("1.0.0"),
		DeviceModel:    ptr("iPhone"),
	}
	res, err := c.RegisterDeviceWithResponse(ctx, reg)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil {
		t.Fatalf("register: %d %s", res.StatusCode(), res.Body)
	}
	if res.JSON200.LoginId != "user-1" || res.JSON200.Id == 0 {
		t.Fatalf("unexpected device: %+v", res.JSON200)
	}

	// 同じ installationId で別ユーザー・別トークン → 付け替え (行は増えない)
	reg.LoginId = "user-2"
	reg.PushToken = "ExponentPushToken[bbb]"
	res2, err := c.RegisterDeviceWithResponse(ctx, reg)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode() != http.StatusOK || res2.JSON200 == nil {
		t.Fatalf("re-register: %d %s", res2.StatusCode(), res2.Body)
	}
	if res2.JSON200.Id != res.JSON200.Id {
		t.Fatalf("expected same row id, got %d and %d", res.JSON200.Id, res2.JSON200.Id)
	}
	if res2.JSON200.LoginId != "user-2" || res2.JSON200.PushToken != "ExponentPushToken[bbb]" {
		t.Fatalf("not updated: %+v", res2.JSON200)
	}
	if n := client.Device.Query().CountX(ctx); n != 1 {
		t.Fatalf("expected 1 device, got %d", n)
	}

	// GET は API キー必須
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
	if get.StatusCode() != http.StatusOK || get.JSON200 == nil || get.JSON200.LoginId != "user-2" {
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

func TestRegisterDevice_Validation(t *testing.T) {
	srv, _ := newTestServer(t, &fakeSender{})

	// platform が enum 外 → OpenAPI バリデーションで 400
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
}

func TestUnregisterDevice(t *testing.T) {
	srv, client := newTestServer(t, &fakeSender{})
	c := newClient(t, srv)
	ctx := context.Background()

	client.Device.Create().
		SetInstallationID("inst-1").SetLoginID("u").SetPlatform("android").SetPushToken("t").
		SaveX(ctx)

	res, err := c.UnregisterDeviceWithResponse(ctx, "inst-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("expected 204, got %d %s", res.StatusCode(), res.Body)
	}
	if n := client.Device.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected 0 devices, got %d", n)
	}
	// 存在しなくても 204
	res, err = c.UnregisterDeviceWithResponse(ctx, "inst-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.StatusCode())
	}
}

func TestSendNotification(t *testing.T) {
	sender := &fakeSender{
		results: func(msgs []push.Message) []push.Result {
			out := make([]push.Result, len(msgs))
			for i, m := range msgs {
				switch m.To {
				case "dead":
					out[i] = push.Result{Error: "DeviceNotRegistered", Unregistered: true}
				default:
					out[i] = push.Result{OK: true}
				}
			}
			return out
		},
	}
	srv, client := newTestServer(t, sender)
	c := newClient(t, srv)
	ctx := context.Background()

	client.Device.Create().SetInstallationID("a").SetLoginID("u1").SetPlatform("ios").SetPushToken("tok-a").SaveX(ctx)
	client.Device.Create().SetInstallationID("b").SetLoginID("u1").SetPlatform("android").SetPushToken("dead").SaveX(ctx)
	client.Device.Create().SetInstallationID("c").SetLoginID("u2").SetPlatform("ios").SetPushToken("tok-c").SaveX(ctx)
	client.Device.Create().SetInstallationID("d").SetLoginID("other").SetPlatform("ios").SetPushToken("tok-d").SaveX(ctx)

	req := api.SendNotificationRequest{
		LoginIds: []string{"u1", "u2"},
		Title:    "hello",
		Body:     ptr("world"),
		Url:      ptr("https://example.com/inbox"),
		Data:     &map[string]any{"kind": "message"},
	}

	// API キーなし → 401
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
	if r.Requested != 3 || r.Sent != 2 || r.Failed != 1 {
		t.Fatalf("unexpected counts: %+v", r)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 3 {
		t.Fatalf("unexpected sent batches: %+v", sender.sent)
	}
	m := sender.sent[0][0]
	if m.Title != "hello" || m.Body != "world" || m.Data["url"] != "https://example.com/inbox" || m.Data["kind"] != "message" {
		t.Fatalf("unexpected message: %+v", m)
	}
	// 失効した端末は削除される
	for _, dr := range r.Results {
		if dr.InstallationId == "b" {
			if dr.Status != api.DeliveryResultStatusError || dr.Unregistered == nil || !*dr.Unregistered {
				t.Fatalf("expected unregistered error for b: %+v", dr)
			}
		}
	}
	if n := client.Device.Query().CountX(ctx); n != 3 {
		t.Fatalf("expected dead device deleted, got %d devices", n)
	}

	// 宛先なし
	none, err := c.SendNotificationWithResponse(ctx, api.SendNotificationRequest{LoginIds: []string{"nobody"}, Title: "x"}, apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if none.StatusCode() != http.StatusOK || none.JSON200.Requested != 0 {
		t.Fatalf("expected empty result, got %d %s", none.StatusCode(), none.Body)
	}
}

package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/nonchan7720/webapp-notification/backend/internal/api"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent/device"
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

// createDevice は端末を作り、loginIDs をそれぞれ紐付ける (テストのセットアップ用)。
func createDevice(t *testing.T, client *ent.Client, installationID, platform, pushToken string, loginIDs ...string) *ent.Device {
	t.Helper()
	ctx := context.Background()
	d := client.Device.Create().
		SetInstallationID(installationID).
		SetPlatform(device.Platform(platform)).
		SetPushToken(pushToken).
		SaveX(ctx)
	for _, l := range loginIDs {
		client.DeviceLogin.Create().SetDeviceID(d.ID).SetLoginID(l).SaveX(ctx)
	}
	return d
}

// sortedStrings はテストの比較用に文字列スライスをソートしたコピーを返す。
func sortedStrings(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

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
		PushToken:      ptr("ExponentPushToken[aaa]"),
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
	if !reflect.DeepEqual(res.JSON200.LoginIds, []string{"user-1"}) || res.JSON200.Id == 0 {
		t.Fatalf("unexpected device: %+v", res.JSON200)
	}

	// 同じ installationId で別ログイン ID・別トークンで再登録 → 端末は 1 行のまま、
	// ログイン ID は多対多で「追加」される (置き換わらない)。
	reg.LoginId = "user-2"
	reg.PushToken = ptr("ExponentPushToken[bbb]")
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
	if !reflect.DeepEqual(sortedStrings(res2.JSON200.LoginIds), []string{"user-1", "user-2"}) {
		t.Fatalf("expected both loginIds, got %+v", res2.JSON200.LoginIds)
	}
	if res2.JSON200.PushToken == nil || *res2.JSON200.PushToken != "ExponentPushToken[bbb]" {
		t.Fatalf("push token not updated: %+v", res2.JSON200)
	}
	if n := client.Device.Query().CountX(ctx); n != 1 {
		t.Fatalf("expected 1 device, got %d", n)
	}
	if n := client.DeviceLogin.Query().CountX(ctx); n != 2 {
		t.Fatalf("expected 2 device_logins rows, got %d", n)
	}

	// 同じ (installationId, loginId) で再登録しても増えない (べき等)。
	res3, err := c.RegisterDeviceWithResponse(ctx, reg)
	if err != nil {
		t.Fatal(err)
	}
	if res3.StatusCode() != http.StatusOK {
		t.Fatalf("re-register (same login): %d %s", res3.StatusCode(), res3.Body)
	}
	if n := client.DeviceLogin.Query().CountX(ctx); n != 2 {
		t.Fatalf("expected still 2 device_logins rows, got %d", n)
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
	if get.StatusCode() != http.StatusOK || get.JSON200 == nil ||
		!reflect.DeepEqual(sortedStrings(get.JSON200.LoginIds), []string{"user-1", "user-2"}) {
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

func TestRegisterDevice_RequiresPushOrDeviceToken(t *testing.T) {
	srv, client := newTestServer(t, &fakeSender{})
	c := newClient(t, srv)
	ctx := context.Background()

	// pushToken も deviceToken もなし → 400 invalid_request
	res, err := c.RegisterDeviceWithResponse(ctx, api.DeviceRegistration{
		LoginId:        "u",
		InstallationId: "inst-none",
		Platform:       api.Android,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusBadRequest || res.JSON400 == nil {
		t.Fatalf("expected 400, got %d %s", res.StatusCode(), res.Body)
	}
	if res.JSON400.Code != "invalid_request" {
		t.Fatalf("unexpected error body: %+v", res.JSON400)
	}
	if n := client.Device.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected no device created, got %d", n)
	}

	// deviceToken だけ (native モード) → OK
	res2, err := c.RegisterDeviceWithResponse(ctx, api.DeviceRegistration{
		LoginId:        "u",
		InstallationId: "inst-native",
		Platform:       api.Android,
		DeviceToken:    ptr("fcm-registration-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode() != http.StatusOK || res2.JSON200 == nil {
		t.Fatalf("register (deviceToken only): %d %s", res2.StatusCode(), res2.Body)
	}
	if res2.JSON200.PushToken != nil {
		t.Fatalf("expected no pushToken, got %+v", res2.JSON200.PushToken)
	}
	if res2.JSON200.DeviceToken == nil || *res2.JSON200.DeviceToken != "fcm-registration-token" {
		t.Fatalf("unexpected deviceToken: %+v", res2.JSON200)
	}
	if !reflect.DeepEqual(res2.JSON200.LoginIds, []string{"u"}) {
		t.Fatalf("unexpected loginIds: %+v", res2.JSON200.LoginIds)
	}
}

func TestUnregisterDevice(t *testing.T) {
	srv, client := newTestServer(t, &fakeSender{})
	c := newClient(t, srv)
	ctx := context.Background()

	// 複数ログイン ID が紐付いていても、端末ごと削除すれば全部消える (ON DELETE CASCADE)。
	createDevice(t, client, "inst-1", "android", "t", "u1", "u2")

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
	if n := client.DeviceLogin.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected device_logins to cascade-delete, got %d", n)
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

func TestUnregisterDeviceLogin(t *testing.T) {
	srv, client := newTestServer(t, &fakeSender{})
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, client, "inst-1", "android", "t", "u1", "u2")

	// u1 の紐付けだけ外す。
	res, err := c.UnregisterDeviceLoginWithResponse(ctx, "inst-1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusNoContent {
		t.Fatalf("expected 204, got %d %s", res.StatusCode(), res.Body)
	}
	// 端末自体は残り、u2 の紐付けも残る。
	if n := client.Device.Query().CountX(ctx); n != 1 {
		t.Fatalf("expected device to remain, got %d", n)
	}
	get, err := c.GetDeviceWithResponse(ctx, "inst-1", apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if get.StatusCode() != http.StatusOK || !reflect.DeepEqual(get.JSON200.LoginIds, []string{"u2"}) {
		t.Fatalf("unexpected loginIds after unlink: %d %+v", get.StatusCode(), get.JSON200)
	}

	// 存在しない紐付けを外しても 204 (端末はある、リンクはもうない)。
	res2, err := c.UnregisterDeviceLoginWithResponse(ctx, "inst-1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode() != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res2.StatusCode())
	}
	// 存在しない installationId でも 204。
	res3, err := c.UnregisterDeviceLoginWithResponse(ctx, "nope", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if res3.StatusCode() != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res3.StatusCode())
	}
}

func TestUnregisterLogin(t *testing.T) {
	srv, client := newTestServer(t, &fakeSender{})
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, client, "inst-1", "android", "t1", "u1", "u2")
	createDevice(t, client, "inst-2", "ios", "t2", "u1")
	createDevice(t, client, "inst-3", "ios", "t3", "u3")

	// API キーなし → 401。
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
	// 両方の端末は残るが、u1 の紐付けだけ消える。
	if n := client.Device.Query().CountX(ctx); n != 3 {
		t.Fatalf("expected devices to remain, got %d", n)
	}
	if n := client.DeviceLogin.Query().CountX(ctx); n != 2 { // u2 (inst-1) と u3 (inst-3)
		t.Fatalf("expected 2 remaining device_logins, got %d", n)
	}

	// もう一度呼んでも 0 件で 200。
	res2, err := c.UnregisterLoginWithResponse(ctx, "u1", apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode() != http.StatusOK || res2.JSON200 == nil || res2.JSON200.Removed != 0 {
		t.Fatalf("unexpected result: %d %+v", res2.StatusCode(), res2.JSON200)
	}
}

func TestSendNotification(t *testing.T) {
	sender := &fakeSender{
		results: func(msgs []push.Message) []push.Result {
			out := make([]push.Result, len(msgs))
			for i, m := range msgs {
				switch m.ExpoToken {
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

	createDevice(t, client, "a", "ios", "tok-a", "u1")
	createDevice(t, client, "b", "android", "dead", "u1")
	createDevice(t, client, "c", "ios", "tok-c", "u2")
	createDevice(t, client, "d", "ios", "tok-d", "other")

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
	if m.Platform != "ios" || m.ExpoToken != "tok-a" {
		t.Fatalf("unexpected message platform/expoToken: %+v", m)
	}
	// 失効した端末は削除される。各結果の loginIds もリクエストと一致するものだけになっている。
	for _, dr := range r.Results {
		switch dr.InstallationId {
		case "a":
			if !reflect.DeepEqual(dr.LoginIds, []string{"u1"}) {
				t.Fatalf("unexpected loginIds for a: %+v", dr)
			}
		case "b":
			if dr.Status != api.DeliveryResultStatusError || dr.Unregistered == nil || !*dr.Unregistered {
				t.Fatalf("expected unregistered error for b: %+v", dr)
			}
			if !reflect.DeepEqual(dr.LoginIds, []string{"u1"}) {
				t.Fatalf("unexpected loginIds for b: %+v", dr)
			}
		case "c":
			if !reflect.DeepEqual(dr.LoginIds, []string{"u2"}) {
				t.Fatalf("unexpected loginIds for c: %+v", dr)
			}
		case "d":
			t.Fatalf("device d (login %q) should not have been targeted", "other")
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

// TestSendNotification_NativeDevice は pushToken / deviceToken の両方が
// 登録されている端末について、push.Message が platform / expoToken /
// deviceToken を正しく埋めることを確認する
// (PUSH_PROVIDER=native の NativeSender が使う情報)。
func TestSendNotification_NativeDevice(t *testing.T) {
	sender := &fakeSender{}
	srv, client := newTestServer(t, sender)
	c := newClient(t, srv)
	ctx := context.Background()

	client.Device.Create().
		SetInstallationID("native-1").
		SetPlatform("android").
		SetPushToken("ExponentPushToken[aaa]").
		SetDeviceToken("fcm-registration-token").
		SaveX(ctx)
	device1 := client.Device.Query().OnlyX(ctx)
	client.DeviceLogin.Create().SetDeviceID(device1.ID).SetLoginID("u1").SaveX(ctx)

	req := api.SendNotificationRequest{LoginIds: []string{"u1"}, Title: "hello"}
	res, err := c.SendNotificationWithResponse(ctx, req, apiKeyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil || res.JSON200.Sent != 1 {
		t.Fatalf("send: %d %s", res.StatusCode(), res.Body)
	}
	if len(sender.sent) != 1 || len(sender.sent[0]) != 1 {
		t.Fatalf("unexpected sent batches: %+v", sender.sent)
	}
	m := sender.sent[0][0]
	if m.Platform != "android" {
		t.Fatalf("unexpected platform: %+v", m)
	}
	if m.ExpoToken != "ExponentPushToken[aaa]" {
		t.Fatalf("unexpected expoToken: %+v", m)
	}
	if m.DeviceToken != "fcm-registration-token" {
		t.Fatalf("unexpected deviceToken: %+v", m)
	}
}

// TestSendNotification_DedupesPerDevice は、1 端末に複数のログイン ID (多対多)
// が紐付いていて、リクエストがその両方を宛先に含む場合でも、通知は 1 通だけ
// 送られ、結果行も 1 つにまとまり loginIds に両方が入ることを確認する。
func TestSendNotification_DedupesPerDevice(t *testing.T) {
	sender := &fakeSender{}
	srv, client := newTestServer(t, sender)
	c := newClient(t, srv)
	ctx := context.Background()

	createDevice(t, client, "shared", "ios", "tok", "u1", "u2")
	// 無関係な宛先ではない端末も 1 つ用意しておく。
	createDevice(t, client, "solo", "ios", "tok-solo", "u2")

	req := api.SendNotificationRequest{LoginIds: []string{"u1", "u2"}, Title: "hello"}
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
		t.Fatalf("expected exactly one push per device (2 total), got batches: %+v", sender.sent)
	}
	var sharedResult *api.DeliveryResult
	for i := range r.Results {
		if r.Results[i].InstallationId == "shared" {
			sharedResult = &r.Results[i]
		}
	}
	if sharedResult == nil {
		t.Fatalf("no result for shared device: %+v", r.Results)
	}
	if !reflect.DeepEqual(sortedStrings(sharedResult.LoginIds), []string{"u1", "u2"}) {
		t.Fatalf("expected both loginIds on the single result, got %+v", sharedResult.LoginIds)
	}
}

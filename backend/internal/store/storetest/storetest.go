// Package storetest is a shared contract test suite for core.Store
// implementations (internal/store/entstore, internal/store/sqlstore): both
// are run against it so they stay behaviorally interchangeable.
package storetest

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/nonchan7720/pushshell/backend/internal/core"
)

// RunStoreTests runs the full contract suite against a fresh store built by
// newStore for each subtest.
func RunStoreTests(t *testing.T, newStore func(t *testing.T) core.Store) {
	t.Helper()

	t.Run("UpsertDevice_CreateAndUpdate", func(t *testing.T) { testUpsertDeviceCreateAndUpdate(t, newStore(t)) })
	t.Run("UpsertDevice_PartialUpdateKeepsExisting", func(t *testing.T) { testUpsertDevicePartialUpdate(t, newStore(t)) })
	t.Run("GetDevice_NotFound", func(t *testing.T) { testGetDeviceNotFound(t, newStore(t)) })
	t.Run("LinkLogin", func(t *testing.T) { testLinkLogin(t, newStore(t)) })
	t.Run("LinkLogin_NotFound", func(t *testing.T) { testLinkLoginNotFound(t, newStore(t)) })
	t.Run("UnlinkLogin", func(t *testing.T) { testUnlinkLogin(t, newStore(t)) })
	t.Run("UnlinkLoginEverywhere", func(t *testing.T) { testUnlinkLoginEverywhere(t, newStore(t)) })
	t.Run("DeleteDevice_Cascade", func(t *testing.T) { testDeleteDeviceCascade(t, newStore(t)) })
	t.Run("DeleteDevice_Idempotent", func(t *testing.T) { testDeleteDeviceIdempotent(t, newStore(t)) })
	t.Run("FindDevicesByLogins", func(t *testing.T) { testFindDevicesByLogins(t, newStore(t)) })
	t.Run("FindDevicesByLogins_Empty", func(t *testing.T) { testFindDevicesByLoginsEmpty(t, newStore(t)) })
	t.Run("FindDevicesByInstallationIDs", func(t *testing.T) { testFindDevicesByInstallationIDs(t, newStore(t)) })
	t.Run("FindDevicesByInstallationIDs_Empty", func(t *testing.T) { testFindDevicesByInstallationIDsEmpty(t, newStore(t)) })
	t.Run("ListDevices", func(t *testing.T) { testListDevices(t, newStore(t)) })
	t.Run("ListDevices_PlatformFilter", func(t *testing.T) { testListDevicesPlatformFilter(t, newStore(t)) })
	t.Run("ListDevices_LocaleFilter", func(t *testing.T) { testListDevicesLocaleFilter(t, newStore(t)) })
	t.Run("ListDevices_PlatformAndLocaleFilter", func(t *testing.T) { testListDevicesPlatformAndLocaleFilter(t, newStore(t)) })
	t.Run("ListDevices_Empty", func(t *testing.T) { testListDevicesEmpty(t, newStore(t)) })
}

func testUpsertDeviceCreateAndUpdate(t *testing.T, s core.Store) {
	ctx := context.Background()

	d, err := s.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: "inst-1", Platform: "ios", PushToken: "tok-1", AppVersion: "1.0.0",
	})
	if err != nil {
		t.Fatalf("upsert (create): %v", err)
	}
	if d.ID == 0 || d.InstallationID != "inst-1" || d.Platform != "ios" || d.PushToken != "tok-1" || d.AppVersion != "1.0.0" {
		t.Fatalf("unexpected device: %+v", d)
	}
	if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		t.Fatalf("expected created_at/updated_at to be set: %+v", d)
	}

	// Upserting again with the same installationId updates the row in place
	// (does not create a second one) and overwrites fields that are given.
	d2, err := s.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: "inst-1", Platform: "ios", PushToken: "tok-2", AppVersion: "2.0.0",
	})
	if err != nil {
		t.Fatalf("upsert (update): %v", err)
	}
	if d2.ID != d.ID {
		t.Fatalf("expected same id, got %d and %d", d.ID, d2.ID)
	}
	if d2.PushToken != "tok-2" || d2.AppVersion != "2.0.0" {
		t.Fatalf("fields not updated: %+v", d2)
	}

	got, err := s.GetDevice(ctx, "inst-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if got.ID != d.ID || got.PushToken != "tok-2" {
		t.Fatalf("unexpected device after upsert: %+v", got)
	}
}

func testUpsertDevicePartialUpdate(t *testing.T, s core.Store) {
	ctx := context.Background()

	if _, err := s.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: "inst-1", Platform: "ios", PushToken: "tok-1", DeviceModel: "iPhone",
	}); err != nil {
		t.Fatalf("upsert (create): %v", err)
	}

	// Re-upserting without deviceModel/pushToken (both left "") must not
	// clear the existing values — only fields actually supplied change.
	d, err := s.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: "inst-1", Platform: "ios", DeviceToken: "device-tok",
	})
	if err != nil {
		t.Fatalf("upsert (partial): %v", err)
	}
	if d.PushToken != "tok-1" {
		t.Fatalf("expected pushToken to be kept, got %q", d.PushToken)
	}
	if d.DeviceModel != "iPhone" {
		t.Fatalf("expected deviceModel to be kept, got %q", d.DeviceModel)
	}
	if d.DeviceToken != "device-tok" {
		t.Fatalf("expected deviceToken to be set, got %q", d.DeviceToken)
	}
}

func testGetDeviceNotFound(t *testing.T, s core.Store) {
	ctx := context.Background()
	if _, err := s.GetDevice(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func testLinkLogin(t *testing.T, s core.Store) {
	ctx := context.Background()
	if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: "inst-1", Platform: "ios", PushToken: "t"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := s.LinkLogin(ctx, "inst-1", "u1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// Idempotent: linking the same (installationId, loginId) again must not
	// error or duplicate.
	if err := s.LinkLogin(ctx, "inst-1", "u1"); err != nil {
		t.Fatalf("link (again): %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-1", "u2"); err != nil {
		t.Fatalf("link (second login): %v", err)
	}

	d, err := s.GetDevice(ctx, "inst-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if !reflect.DeepEqual(sortedCopy(d.LoginIDs), []string{"u1", "u2"}) {
		t.Fatalf("unexpected loginIds: %+v", d.LoginIDs)
	}
}

func testLinkLoginNotFound(t *testing.T, s core.Store) {
	ctx := context.Background()
	if err := s.LinkLogin(ctx, "nope", "u1"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func testUnlinkLogin(t *testing.T, s core.Store) {
	ctx := context.Background()
	if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: "inst-1", Platform: "ios", PushToken: "t"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-1", "u1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-1", "u2"); err != nil {
		t.Fatalf("link: %v", err)
	}

	if err := s.UnlinkLogin(ctx, "inst-1", "u1"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	d, err := s.GetDevice(ctx, "inst-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if !reflect.DeepEqual(d.LoginIDs, []string{"u2"}) {
		t.Fatalf("unexpected loginIds after unlink: %+v", d.LoginIDs)
	}

	// Unlinking an absent link, or from an absent device, is not an error.
	if err := s.UnlinkLogin(ctx, "inst-1", "u1"); err != nil {
		t.Fatalf("unlink (already gone): %v", err)
	}
	if err := s.UnlinkLogin(ctx, "nope", "u1"); err != nil {
		t.Fatalf("unlink (no device): %v", err)
	}
}

func testUnlinkLoginEverywhere(t *testing.T, s core.Store) {
	ctx := context.Background()
	for _, inst := range []string{"inst-1", "inst-2", "inst-3"} {
		if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: inst, Platform: "ios", PushToken: "t"}); err != nil {
			t.Fatalf("upsert %s: %v", inst, err)
		}
	}
	if err := s.LinkLogin(ctx, "inst-1", "u1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-1", "u2"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-2", "u1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-3", "u3"); err != nil {
		t.Fatalf("link: %v", err)
	}

	n, err := s.UnlinkLoginEverywhere(ctx, "u1")
	if err != nil {
		t.Fatalf("unlink everywhere: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 removed, got %d", n)
	}

	// Devices themselves are not deleted.
	if _, err := s.GetDevice(ctx, "inst-1"); err != nil {
		t.Fatalf("inst-1 should remain: %v", err)
	}
	d2, err := s.GetDevice(ctx, "inst-2")
	if err != nil {
		t.Fatalf("get inst-2: %v", err)
	}
	if len(d2.LoginIDs) != 0 {
		t.Fatalf("expected inst-2 to have no logins left, got %+v", d2.LoginIDs)
	}

	n2, err := s.UnlinkLoginEverywhere(ctx, "u1")
	if err != nil {
		t.Fatalf("unlink everywhere (again): %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 removed the second time, got %d", n2)
	}
}

func testDeleteDeviceCascade(t *testing.T, s core.Store) {
	ctx := context.Background()
	if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: "inst-1", Platform: "ios", PushToken: "t"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-1", "u1"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-1", "u2"); err != nil {
		t.Fatalf("link: %v", err)
	}

	if err := s.DeleteDevice(ctx, "inst-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetDevice(ctx, "inst-1"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	// The login links must have cascade-deleted too: re-registering a new
	// device under a login that used to point at the deleted one must not
	// see any stale links.
	if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: "inst-2", Platform: "ios", PushToken: "t2"}); err != nil {
		t.Fatalf("upsert inst-2: %v", err)
	}
	if err := s.LinkLogin(ctx, "inst-2", "u1"); err != nil {
		t.Fatalf("link inst-2/u1: %v", err)
	}
	matches, err := s.FindDevicesByLogins(ctx, []string{"u1"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(matches) != 1 || matches[0].Device.InstallationID != "inst-2" {
		t.Fatalf("expected only inst-2 to match u1 (cascade should have removed inst-1's link), got %+v", matches)
	}
}

func testDeleteDeviceIdempotent(t *testing.T, s core.Store) {
	ctx := context.Background()
	if err := s.DeleteDevice(ctx, "nope"); err != nil {
		t.Fatalf("delete (absent): %v", err)
	}
}

func testFindDevicesByLogins(t *testing.T, s core.Store) {
	ctx := context.Background()
	seed := func(inst, platform, tok string, logins ...string) {
		t.Helper()
		if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: inst, Platform: platform, PushToken: tok}); err != nil {
			t.Fatalf("upsert %s: %v", inst, err)
		}
		for _, l := range logins {
			if err := s.LinkLogin(ctx, inst, l); err != nil {
				t.Fatalf("link %s/%s: %v", inst, l, err)
			}
		}
	}
	seed("shared", "ios", "tok-shared", "u1", "u2")
	seed("solo", "ios", "tok-solo", "u2")
	seed("other", "android", "tok-other", "u3")

	matches, err := s.FindDevicesByLogins(ctx, []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %+v", matches)
	}
	// Ordered by device id ascending: "shared" was created before "solo".
	if matches[0].Device.InstallationID != "shared" || matches[1].Device.InstallationID != "solo" {
		t.Fatalf("unexpected order: %+v", matches)
	}
	if !reflect.DeepEqual(sortedCopy(matches[0].LoginIDs), []string{"u1", "u2"}) {
		t.Fatalf("unexpected matched loginIds for shared: %+v", matches[0].LoginIDs)
	}
	if !reflect.DeepEqual(matches[1].LoginIDs, []string{"u2"}) {
		t.Fatalf("unexpected matched loginIds for solo: %+v", matches[1].LoginIDs)
	}

	none, err := s.FindDevicesByLogins(ctx, []string{"nobody"})
	if err != nil {
		t.Fatalf("find (no match): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no matches, got %+v", none)
	}
}

func testFindDevicesByLoginsEmpty(t *testing.T, s core.Store) {
	ctx := context.Background()
	matches, err := s.FindDevicesByLogins(ctx, nil)
	if err != nil {
		t.Fatalf("find (empty): %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %+v", matches)
	}
}

// seedDevice upserts a device (with locale, which may be "") and links it to
// logins.
func seedDevice(t *testing.T, s core.Store, inst, platform, locale string, logins ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.UpsertDevice(ctx, core.DeviceInput{
		InstallationID: inst, Platform: platform, PushToken: "tok-" + inst, Locale: locale,
	}); err != nil {
		t.Fatalf("upsert %s: %v", inst, err)
	}
	for _, l := range logins {
		if err := s.LinkLogin(ctx, inst, l); err != nil {
			t.Fatalf("link %s/%s: %v", inst, l, err)
		}
	}
}

// installationIDsOf returns the installation IDs of devices, in order.
func installationIDsOf(devices []core.Device) []string {
	out := make([]string, len(devices))
	for i, d := range devices {
		out[i] = d.InstallationID
	}
	return out
}

func testFindDevicesByInstallationIDs(t *testing.T, s core.Store) {
	ctx := context.Background()
	seedDevice(t, s, "first", "ios", "ja-JP", "u2", "u1")
	seedDevice(t, s, "second", "android", "en-US") // no logins
	seedDevice(t, s, "third", "ios", "", "u3")
	seedDevice(t, s, "fourth", "android", "", "u1")

	// Ordered by device ID ascending regardless of the requested order;
	// unknown and duplicate installation IDs are skipped / collapsed.
	devices, err := s.FindDevicesByInstallationIDs(ctx, []string{"third", "nope", "first", "second", "third"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got := installationIDsOf(devices); !reflect.DeepEqual(got, []string{"first", "second", "third"}) {
		t.Fatalf("unexpected devices/order: %v", got)
	}
	if devices[0].ID >= devices[1].ID || devices[1].ID >= devices[2].ID {
		t.Fatalf("expected ascending device IDs: %+v", devices)
	}
	// LoginIDs populated and sorted; all of the device's logins, not only
	// requested ones (there is no login filter here).
	if !reflect.DeepEqual(devices[0].LoginIDs, []string{"u1", "u2"}) {
		t.Fatalf("unexpected loginIds for first: %+v", devices[0].LoginIDs)
	}
	if len(devices[1].LoginIDs) != 0 {
		t.Fatalf("expected no loginIds for second: %+v", devices[1].LoginIDs)
	}
	if !reflect.DeepEqual(devices[2].LoginIDs, []string{"u3"}) {
		t.Fatalf("unexpected loginIds for third: %+v", devices[2].LoginIDs)
	}
	// Full device fields are populated.
	if devices[0].Platform != "ios" || devices[0].PushToken != "tok-first" || devices[0].Locale != "ja-JP" || devices[0].CreatedAt.IsZero() {
		t.Fatalf("unexpected device fields: %+v", devices[0])
	}

	none, err := s.FindDevicesByInstallationIDs(ctx, []string{"nope", "nada"})
	if err != nil {
		t.Fatalf("find (unknown): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no devices, got %+v", none)
	}
}

func testFindDevicesByInstallationIDsEmpty(t *testing.T, s core.Store) {
	ctx := context.Background()
	seedDevice(t, s, "a", "ios", "", "u1")
	for _, in := range [][]string{nil, {}} {
		devices, err := s.FindDevicesByInstallationIDs(ctx, in)
		if err != nil {
			t.Fatalf("find (empty): %v", err)
		}
		if devices != nil {
			t.Fatalf("expected nil for empty input, got %+v", devices)
		}
	}
}

func testListDevices(t *testing.T, s core.Store) {
	ctx := context.Background()
	seedDevice(t, s, "d1", "ios", "ja-JP", "u2", "u1")
	seedDevice(t, s, "d2", "android", "")
	seedDevice(t, s, "d3", "ios", "en-US", "u3")

	devices, err := s.ListDevices(ctx, core.DeviceFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := installationIDsOf(devices); !reflect.DeepEqual(got, []string{"d1", "d2", "d3"}) {
		t.Fatalf("unexpected devices/order: %v", got)
	}
	if !reflect.DeepEqual(devices[0].LoginIDs, []string{"u1", "u2"}) || len(devices[1].LoginIDs) != 0 ||
		!reflect.DeepEqual(devices[2].LoginIDs, []string{"u3"}) {
		t.Fatalf("unexpected loginIds: %+v", devices)
	}
	if devices[0].Locale != "ja-JP" || devices[1].Locale != "" || devices[2].Platform != "ios" {
		t.Fatalf("unexpected device fields: %+v", devices)
	}
}

func testListDevicesPlatformFilter(t *testing.T, s core.Store) {
	ctx := context.Background()
	seedDevice(t, s, "i1", "ios", "", "u1")
	seedDevice(t, s, "a1", "android", "", "u1")
	seedDevice(t, s, "i2", "ios", "")

	ios, err := s.ListDevices(ctx, core.DeviceFilter{Platforms: []string{"ios"}})
	if err != nil {
		t.Fatalf("list ios: %v", err)
	}
	if got := installationIDsOf(ios); !reflect.DeepEqual(got, []string{"i1", "i2"}) {
		t.Fatalf("ios: %v", got)
	}
	if !reflect.DeepEqual(ios[0].LoginIDs, []string{"u1"}) {
		t.Fatalf("ios loginIds: %+v", ios[0].LoginIDs)
	}

	both, err := s.ListDevices(ctx, core.DeviceFilter{Platforms: []string{"android", "ios"}})
	if err != nil {
		t.Fatalf("list both: %v", err)
	}
	if got := installationIDsOf(both); !reflect.DeepEqual(got, []string{"i1", "a1", "i2"}) {
		t.Fatalf("both platforms: %v", got)
	}
}

func testListDevicesLocaleFilter(t *testing.T, s core.Store) {
	ctx := context.Background()
	seedDevice(t, s, "ja", "ios", "ja", "u1")
	seedDevice(t, s, "ja-jp", "ios", "ja-JP")
	seedDevice(t, s, "jav", "android", "jav") // plain prefix match: "ja" also matches this one
	seedDevice(t, s, "en-us", "android", "en-US")
	seedDevice(t, s, "none", "android", "")
	seedDevice(t, s, "pct", "android", "x%y_z") // LIKE wildcards in stored data
	seedDevice(t, s, "other", "android", "xay")

	list := func(prefixes ...string) []string {
		t.Helper()
		devices, err := s.ListDevices(ctx, core.DeviceFilter{LocalePrefixes: prefixes})
		if err != nil {
			t.Fatalf("list %v: %v", prefixes, err)
		}
		return installationIDsOf(devices)
	}

	// Case-insensitive prefix match on the stored locale.
	if got := list("JA"); !reflect.DeepEqual(got, []string{"ja", "ja-jp", "jav"}) {
		t.Fatalf("prefix JA: %v", got)
	}
	if got := list("ja-jp"); !reflect.DeepEqual(got, []string{"ja-jp"}) {
		t.Fatalf("prefix ja-jp: %v", got)
	}
	if got := list("EN-us"); !reflect.DeepEqual(got, []string{"en-us"}) {
		t.Fatalf("prefix EN-us: %v", got)
	}
	// Several prefixes are OR-ed; devices without a locale never match.
	if got := list("en", "ja-JP"); !reflect.DeepEqual(got, []string{"ja-jp", "en-us"}) {
		t.Fatalf("prefixes en,ja-JP: %v", got)
	}
	if got := list("zz"); len(got) != 0 {
		t.Fatalf("prefix zz: %v", got)
	}
	// LIKE wildcards in the prefix are matched literally.
	if got := list("x%"); !reflect.DeepEqual(got, []string{"pct"}) {
		t.Fatalf("prefix x%%: %v", got)
	}
	if got := list("x%y_"); !reflect.DeepEqual(got, []string{"pct"}) {
		t.Fatalf("prefix x%%y_: %v", got)
	}
	if got := list("_a"); len(got) != 0 {
		t.Fatalf("prefix _a: %v", got)
	}
}

func testListDevicesPlatformAndLocaleFilter(t *testing.T, s core.Store) {
	ctx := context.Background()
	seedDevice(t, s, "ios-ja", "ios", "ja-JP", "u1")
	seedDevice(t, s, "android-ja", "android", "ja-JP")
	seedDevice(t, s, "ios-en", "ios", "en-US")
	seedDevice(t, s, "android-en", "android", "en-GB")

	devices, err := s.ListDevices(ctx, core.DeviceFilter{Platforms: []string{"ios"}, LocalePrefixes: []string{"ja"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := installationIDsOf(devices); !reflect.DeepEqual(got, []string{"ios-ja"}) {
		t.Fatalf("ios+ja: %v", got)
	}
	if !reflect.DeepEqual(devices[0].LoginIDs, []string{"u1"}) {
		t.Fatalf("loginIds: %+v", devices[0].LoginIDs)
	}

	devices, err = s.ListDevices(ctx, core.DeviceFilter{Platforms: []string{"android"}, LocalePrefixes: []string{"ja", "en"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := installationIDsOf(devices); !reflect.DeepEqual(got, []string{"android-ja", "android-en"}) {
		t.Fatalf("android+ja/en: %v", got)
	}
}

func testListDevicesEmpty(t *testing.T, s core.Store) {
	devices, err := s.ListDevices(context.Background(), core.DeviceFilter{})
	if err != nil {
		t.Fatalf("list (empty store): %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected no devices, got %+v", devices)
	}
}

func sortedCopy(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

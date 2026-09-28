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

	"github.com/nonchan7720/webapp-notification/backend/internal/core"
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

func sortedCopy(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

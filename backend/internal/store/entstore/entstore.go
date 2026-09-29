// Package entstore implements core.Store on top of ent
// (github.com/nonchan7720/pushshell/backend/internal/ent), the
// same way internal/handler used to query the database directly. Only
// cmd/server (and internal/db.Open, sqlite/mysql/postgres) uses this
// package; cmd/worker uses internal/store/sqlstore instead, so the
// Cloudflare Workers/wasm build does not need to link ent or atlas.
package entstore

import (
	"context"
	"fmt"
	"sort"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/nonchan7720/pushshell/backend/internal/core"
	"github.com/nonchan7720/pushshell/backend/internal/ent"
	"github.com/nonchan7720/pushshell/backend/internal/ent/device"
	"github.com/nonchan7720/pushshell/backend/internal/ent/devicelogin"
	"github.com/nonchan7720/pushshell/backend/internal/ent/predicate"
)

// Store implements core.Store on an *ent.Client.
type Store struct {
	db *ent.Client
}

// New wraps db as a core.Store.
func New(db *ent.Client) *Store { return &Store{db: db} }

var _ core.Store = (*Store)(nil)

// UpsertDevice implements core.Store.
func (s *Store) UpsertDevice(ctx context.Context, in core.DeviceInput) (core.Device, error) {
	err := s.db.Device.Create().
		SetInstallationID(in.InstallationID).
		SetPlatform(device.Platform(in.Platform)).
		SetNillablePushToken(nilIfEmpty(in.PushToken)).
		SetNillableDeviceToken(nilIfEmpty(in.DeviceToken)).
		SetNillableAppID(nilIfEmpty(in.AppID)).
		SetNillableAppVersion(nilIfEmpty(in.AppVersion)).
		SetNillableBuildNumber(nilIfEmpty(in.BuildNumber)).
		SetNillableOsVersion(nilIfEmpty(in.OSVersion)).
		SetNillableDeviceModel(nilIfEmpty(in.DeviceModel)).
		SetNillableLocale(nilIfEmpty(in.Locale)).
		OnConflictColumns(device.FieldInstallationID).
		UpdateNewValues().
		Exec(ctx)
	if err != nil {
		return core.Device{}, fmt.Errorf("entstore: upsert device: %w", err)
	}
	d, err := s.db.Device.Query().Where(device.InstallationID(in.InstallationID)).Only(ctx)
	if err != nil {
		return core.Device{}, fmt.Errorf("entstore: load device: %w", err)
	}
	return toDevice(d, nil), nil
}

// LinkLogin implements core.Store.
//
// query-then-create, same as the previous internal/handler.linkLogin: a
// unique constraint violation from a concurrent link of the same
// (device, loginId) is treated as "already linked" and ignored.
func (s *Store) LinkLogin(ctx context.Context, installationID, loginID string) error {
	d, err := s.deviceByInstallationID(ctx, installationID)
	if err != nil {
		return err
	}
	exists, err := s.db.DeviceLogin.Query().
		Where(devicelogin.LoginID(loginID), devicelogin.HasDeviceWith(device.ID(d.ID))).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("entstore: check link: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := s.db.DeviceLogin.Create().SetDeviceID(d.ID).SetLoginID(loginID).Save(ctx); err != nil && !ent.IsConstraintError(err) {
		return fmt.Errorf("entstore: link login: %w", err)
	}
	return nil
}

// UnlinkLogin implements core.Store.
func (s *Store) UnlinkLogin(ctx context.Context, installationID, loginID string) error {
	d, err := s.db.Device.Query().Where(device.InstallationID(installationID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("entstore: load device: %w", err)
	}
	if _, err := s.db.DeviceLogin.Delete().
		Where(devicelogin.LoginID(loginID), devicelogin.HasDeviceWith(device.ID(d.ID))).
		Exec(ctx); err != nil {
		return fmt.Errorf("entstore: unlink login: %w", err)
	}
	return nil
}

// UnlinkLoginEverywhere implements core.Store.
func (s *Store) UnlinkLoginEverywhere(ctx context.Context, loginID string) (int, error) {
	n, err := s.db.DeviceLogin.Delete().Where(devicelogin.LoginID(loginID)).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("entstore: unlink login everywhere: %w", err)
	}
	return n, nil
}

// DeleteDevice implements core.Store. A bulk delete (rather than
// Query+DeleteOne) is idempotent by construction: deleting zero rows is not
// an error. The device_logins rows cascade at the DB level (see
// internal/ent/schema/device.go's edge.To(...).Annotations(entsql.OnDelete(entsql.Cascade))).
func (s *Store) DeleteDevice(ctx context.Context, installationID string) error {
	if _, err := s.db.Device.Delete().Where(device.InstallationID(installationID)).Exec(ctx); err != nil {
		return fmt.Errorf("entstore: delete device: %w", err)
	}
	return nil
}

// GetDevice implements core.Store.
func (s *Store) GetDevice(ctx context.Context, installationID string) (core.Device, error) {
	d, err := s.db.Device.Query().Where(device.InstallationID(installationID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return core.Device{}, core.ErrNotFound
		}
		return core.Device{}, fmt.Errorf("entstore: load device: %w", err)
	}
	loginIDs, err := s.loginIDsForDevice(ctx, d.ID)
	if err != nil {
		return core.Device{}, fmt.Errorf("entstore: load logins: %w", err)
	}
	return toDevice(d, loginIDs), nil
}

// FindDevicesByLogins implements core.Store.
func (s *Store) FindDevicesByLogins(ctx context.Context, loginIDs []string) ([]core.DeviceMatch, error) {
	if len(loginIDs) == 0 {
		return nil, nil
	}
	links, err := s.db.DeviceLogin.Query().
		Where(devicelogin.LoginIDIn(loginIDs...)).
		WithDevice().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("entstore: query device logins: %w", err)
	}

	type group struct {
		device   *ent.Device
		loginIDs []string
	}
	groups := make(map[int]*group)
	for _, link := range links {
		d := link.Edges.Device
		if d == nil {
			continue
		}
		g, ok := groups[d.ID]
		if !ok {
			g = &group{device: d}
			groups[d.ID] = g
		}
		g.loginIDs = append(g.loginIDs, link.LoginID)
	}
	ids := make([]int, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	out := make([]core.DeviceMatch, len(ids))
	for i, id := range ids {
		g := groups[id]
		sort.Strings(g.loginIDs)
		out[i] = core.DeviceMatch{Device: toDevice(g.device, nil), LoginIDs: g.loginIDs}
	}
	return out, nil
}

// FindDevicesByInstallationIDs implements core.Store.
func (s *Store) FindDevicesByInstallationIDs(ctx context.Context, installationIDs []string) ([]core.Device, error) {
	if len(installationIDs) == 0 {
		return nil, nil
	}
	return s.queryDevices(ctx, device.InstallationIDIn(installationIDs...))
}

// ListDevices implements core.Store.
//
// The locale pre-filter is ent's dialect-aware case-insensitive prefix
// predicate (sql.FieldHasPrefixFold: LOWER(col) LIKE on sqlite, ILIKE on
// postgres, COLLATE utf8mb4_general_ci LIKE on mysql, with LIKE wildcards in
// the prefix escaped). It is a plain prefix match; core.Service re-applies
// the exact BCP 47 tag-boundary rule.
func (s *Store) ListDevices(ctx context.Context, filter core.DeviceFilter) ([]core.Device, error) {
	var preds []predicate.Device
	if len(filter.Platforms) > 0 {
		platforms := make([]device.Platform, len(filter.Platforms))
		for i, p := range filter.Platforms {
			platforms[i] = device.Platform(p)
		}
		preds = append(preds, device.PlatformIn(platforms...))
	}
	if len(filter.LocalePrefixes) > 0 {
		locales := make([]predicate.Device, len(filter.LocalePrefixes))
		for i, prefix := range filter.LocalePrefixes {
			locales[i] = predicate.Device(entsql.FieldHasPrefixFold(device.FieldLocale, prefix))
		}
		preds = append(preds, device.Or(locales...))
	}
	return s.queryDevices(ctx, preds...)
}

// queryDevices loads the devices matching preds ordered by device ID
// ascending, with LoginIDs populated (sorted; nil if none).
func (s *Store) queryDevices(ctx context.Context, preds ...predicate.Device) ([]core.Device, error) {
	devices, err := s.db.Device.Query().
		Where(preds...).
		Order(ent.Asc(device.FieldID)).
		WithLogins().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("entstore: query devices: %w", err)
	}
	out := make([]core.Device, len(devices))
	for i, d := range devices {
		var loginIDs []string
		for _, l := range d.Edges.Logins {
			loginIDs = append(loginIDs, l.LoginID)
		}
		sort.Strings(loginIDs)
		out[i] = toDevice(d, loginIDs)
	}
	return out, nil
}

func (s *Store) deviceByInstallationID(ctx context.Context, installationID string) (*ent.Device, error) {
	d, err := s.db.Device.Query().Where(device.InstallationID(installationID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, core.ErrNotFound
		}
		return nil, fmt.Errorf("entstore: load device: %w", err)
	}
	return d, nil
}

func (s *Store) loginIDsForDevice(ctx context.Context, deviceID int) ([]string, error) {
	links, err := s.db.DeviceLogin.Query().
		Where(devicelogin.HasDeviceWith(device.ID(deviceID))).
		All(ctx)
	if err != nil {
		return nil, err
	}
	loginIDs := make([]string, len(links))
	for i, l := range links {
		loginIDs[i] = l.LoginID
	}
	sort.Strings(loginIDs)
	return loginIDs, nil
}

func toDevice(d *ent.Device, loginIDs []string) core.Device {
	return core.Device{
		ID:             int64(d.ID),
		InstallationID: d.InstallationID,
		Platform:       string(d.Platform),
		PushToken:      d.PushToken,
		DeviceToken:    d.DeviceToken,
		AppID:          d.AppID,
		AppVersion:     d.AppVersion,
		BuildNumber:    d.BuildNumber,
		OSVersion:      d.OsVersion,
		DeviceModel:    d.DeviceModel,
		Locale:         d.Locale,
		LoginIDs:       loginIDs,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

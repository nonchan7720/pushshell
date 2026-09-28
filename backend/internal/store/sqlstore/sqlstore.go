// Package sqlstore implements core.Store on plain database/sql, with
// hand-written SQL matching the ent-generated schema exactly (tables
// "devices" / "device_logins", see internal/ent/migrate/schema.go and
// migrations/sqlite). It is used by cmd/worker (against Cloudflare D1) so
// the Workers/wasm build does not need to link ent/atlas — internal/store/entstore
// (used by cmd/server) is the ent-backed equivalent, and
// internal/store/storetest runs the same contract tests against both.
//
// No transaction is ever opened: D1 has no interactive transactions
// (driver.Conn.BeginTx always errors), and every method here issues at
// most a couple of independent statements, the same way internal/handler
// never opened one on top of ent either.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nonchan7720/webapp-notification/backend/internal/core"
)

// timeLayout is the wire format created_at/updated_at are stored as: it
// matches what ent's SQLite dialect itself writes for a field.Time (see
// internal/ent/schema/device.go), confirmed against modernc.org/sqlite —
// e.g. "2026-09-28T17:41:41.441300358Z" — so a database written by
// entstore and one written by sqlstore stay interchangeable.
const timeLayout = time.RFC3339Nano

// Store implements core.Store on a *sql.DB. db must already be open on a
// database with the "devices"/"device_logins" schema applied (migrations/sqlite,
// or backend/worker/migrations for D1).
type Store struct {
	db *sql.DB
}

// New wraps db as a core.Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

var _ core.Store = (*Store)(nil)

// UpsertDevice implements core.Store.
//
// COALESCE(excluded.col, devices.col) on conflict reproduces the previous
// ent-based behavior (SetNillableX(nil) leaves a column out of both the
// insert and the OnConflict...UpdateNewValues() update): a field left ""
// in in is not overwritten, an already-registered device keeps its old
// value for it.
func (s *Store) UpsertDevice(ctx context.Context, in core.DeviceInput) (core.Device, error) {
	now := time.Now().UTC().Format(timeLayout)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO devices (
			installation_id, platform, push_token, device_token,
			app_id, app_version, build_number, os_version, device_model, locale,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(installation_id) DO UPDATE SET
			platform     = excluded.platform,
			push_token   = COALESCE(excluded.push_token, devices.push_token),
			device_token = COALESCE(excluded.device_token, devices.device_token),
			app_id       = COALESCE(excluded.app_id, devices.app_id),
			app_version  = COALESCE(excluded.app_version, devices.app_version),
			build_number = COALESCE(excluded.build_number, devices.build_number),
			os_version   = COALESCE(excluded.os_version, devices.os_version),
			device_model = COALESCE(excluded.device_model, devices.device_model),
			locale       = COALESCE(excluded.locale, devices.locale),
			updated_at   = excluded.updated_at
	`,
		in.InstallationID, in.Platform, nullIfEmpty(in.PushToken), nullIfEmpty(in.DeviceToken),
		nullIfEmpty(in.AppID), nullIfEmpty(in.AppVersion), nullIfEmpty(in.BuildNumber),
		nullIfEmpty(in.OSVersion), nullIfEmpty(in.DeviceModel), nullIfEmpty(in.Locale),
		now, now,
	)
	if err != nil {
		return core.Device{}, fmt.Errorf("sqlstore: upsert device: %w", err)
	}
	d, err := s.getDevice(ctx, in.InstallationID)
	if err != nil {
		return core.Device{}, fmt.Errorf("sqlstore: load device: %w", err)
	}
	return d, nil
}

// LinkLogin implements core.Store.
func (s *Store) LinkLogin(ctx context.Context, installationID, loginID string) error {
	deviceID, err := s.deviceIDByInstallationID(ctx, installationID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(timeLayout)
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO device_logins (login_id, created_at, device_logins) VALUES (?, ?, ?)
	`, loginID, now, deviceID); err != nil {
		return fmt.Errorf("sqlstore: link login: %w", err)
	}
	return nil
}

// UnlinkLogin implements core.Store.
func (s *Store) UnlinkLogin(ctx context.Context, installationID, loginID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM device_logins
		WHERE login_id = ? AND device_logins = (SELECT id FROM devices WHERE installation_id = ?)
	`, loginID, installationID); err != nil {
		return fmt.Errorf("sqlstore: unlink login: %w", err)
	}
	return nil
}

// UnlinkLoginEverywhere implements core.Store.
func (s *Store) UnlinkLoginEverywhere(ctx context.Context, loginID string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM device_logins WHERE login_id = ?`, loginID)
	if err != nil {
		return 0, fmt.Errorf("sqlstore: unlink login everywhere: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlstore: rows affected: %w", err)
	}
	return int(n), nil
}

// DeleteDevice implements core.Store. The device_logins rows are deleted
// explicitly (rather than relying on the schema's ON DELETE CASCADE) so
// this does not depend on foreign-key enforcement being on for the
// connection/database it runs against.
func (s *Store) DeleteDevice(ctx context.Context, installationID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM device_logins WHERE device_logins = (SELECT id FROM devices WHERE installation_id = ?)
	`, installationID); err != nil {
		return fmt.Errorf("sqlstore: delete device logins: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE installation_id = ?`, installationID); err != nil {
		return fmt.Errorf("sqlstore: delete device: %w", err)
	}
	return nil
}

// GetDevice implements core.Store.
func (s *Store) GetDevice(ctx context.Context, installationID string) (core.Device, error) {
	return s.getDevice(ctx, installationID)
}

// FindDevicesByLogins implements core.Store.
func (s *Store) FindDevicesByLogins(ctx context.Context, loginIDs []string) ([]core.DeviceMatch, error) {
	if len(loginIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(loginIDs))
	args := make([]any, len(loginIDs))
	for i, id := range loginIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT d.id, d.installation_id, d.platform, d.push_token, d.device_token,
		       d.app_id, d.app_version, d.build_number, d.os_version, d.device_model, d.locale,
		       d.created_at, d.updated_at, dl.login_id
		FROM device_logins dl
		JOIN devices d ON d.id = dl.device_logins
		WHERE dl.login_id IN (%s)
		ORDER BY d.id, dl.login_id
	`, strings.Join(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: find devices by logins: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []core.DeviceMatch
	for rows.Next() {
		d, loginID, err := scanDeviceWithLogin(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlstore: scan device: %w", err)
		}
		if len(out) == 0 || out[len(out)-1].Device.ID != d.ID {
			out = append(out, core.DeviceMatch{Device: d})
		}
		last := &out[len(out)-1]
		last.LoginIDs = append(last.LoginIDs, loginID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: find devices by logins: %w", err)
	}
	return out, nil
}

func (s *Store) getDevice(ctx context.Context, installationID string) (core.Device, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, installation_id, platform, push_token, device_token,
		       app_id, app_version, build_number, os_version, device_model, locale,
		       created_at, updated_at
		FROM devices WHERE installation_id = ?
	`, installationID)
	d, err := scanDevice(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.Device{}, core.ErrNotFound
		}
		return core.Device{}, fmt.Errorf("sqlstore: load device: %w", err)
	}
	loginIDs, err := s.loginIDsForDevice(ctx, d.ID)
	if err != nil {
		return core.Device{}, fmt.Errorf("sqlstore: load logins: %w", err)
	}
	d.LoginIDs = loginIDs
	return d, nil
}

func (s *Store) deviceIDByInstallationID(ctx context.Context, installationID string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM devices WHERE installation_id = ?`, installationID).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, core.ErrNotFound
		}
		return 0, fmt.Errorf("sqlstore: load device: %w", err)
	}
	return id, nil
}

func (s *Store) loginIDsForDevice(ctx context.Context, deviceID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT login_id FROM device_logins WHERE device_logins = ? ORDER BY login_id
	`, deviceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var loginIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		loginIDs = append(loginIDs, id)
	}
	return loginIDs, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanDevice(sc scanner) (core.Device, error) {
	var (
		id                                                                                     int64
		installationID, platform                                                               string
		pushToken, deviceToken, appID, appVersion, buildNumber, osVersion, deviceModel, locale sql.NullString
		createdAt, updatedAt                                                                   string
	)
	if err := sc.Scan(
		&id, &installationID, &platform, &pushToken, &deviceToken,
		&appID, &appVersion, &buildNumber, &osVersion, &deviceModel, &locale,
		&createdAt, &updatedAt,
	); err != nil {
		return core.Device{}, err
	}
	return toDevice(id, installationID, platform, pushToken, deviceToken,
		appID, appVersion, buildNumber, osVersion, deviceModel, locale, createdAt, updatedAt)
}

func scanDeviceWithLogin(sc scanner) (core.Device, string, error) {
	var (
		id                                                                                     int64
		installationID, platform                                                               string
		pushToken, deviceToken, appID, appVersion, buildNumber, osVersion, deviceModel, locale sql.NullString
		createdAt, updatedAt, loginID                                                          string
	)
	if err := sc.Scan(
		&id, &installationID, &platform, &pushToken, &deviceToken,
		&appID, &appVersion, &buildNumber, &osVersion, &deviceModel, &locale,
		&createdAt, &updatedAt, &loginID,
	); err != nil {
		return core.Device{}, "", err
	}
	d, err := toDevice(id, installationID, platform, pushToken, deviceToken,
		appID, appVersion, buildNumber, osVersion, deviceModel, locale, createdAt, updatedAt)
	return d, loginID, err
}

func toDevice(
	id int64, installationID, platform string,
	pushToken, deviceToken, appID, appVersion, buildNumber, osVersion, deviceModel, locale sql.NullString,
	createdAt, updatedAt string,
) (core.Device, error) {
	ca, err := time.Parse(timeLayout, createdAt)
	if err != nil {
		return core.Device{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}
	ua, err := time.Parse(timeLayout, updatedAt)
	if err != nil {
		return core.Device{}, fmt.Errorf("parse updated_at %q: %w", updatedAt, err)
	}
	return core.Device{
		ID:             id,
		InstallationID: installationID,
		Platform:       platform,
		PushToken:      pushToken.String,
		DeviceToken:    deviceToken.String,
		AppID:          appID.String,
		AppVersion:     appVersion.String,
		BuildNumber:    buildNumber.String,
		OSVersion:      osVersion.String,
		DeviceModel:    deviceModel.String,
		Locale:         locale.String,
		CreatedAt:      ca,
		UpdatedAt:      ua,
	}, nil
}

// nullIfEmpty returns nil (SQL NULL) for "", or s itself otherwise, so an
// empty DeviceInput field is bound as NULL — see UpsertDevice's doc comment
// for why that matters for the ON CONFLICT DO UPDATE.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

package core

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Store methods (and Service methods that wrap
// them) when the requested Device does not exist.
var ErrNotFound = errors.New("core: not found")

// Store is the persistence abstraction Service is built on. It has two
// implementations: internal/store/entstore (ent, used by cmd/server) and
// internal/store/sqlstore (database/sql with hand-written SQL, used by
// cmd/worker so the Workers/wasm build does not need to link ent/atlas).
// internal/store/storetest has a shared contract test suite both
// implementations are run against.
type Store interface {
	// UpsertDevice creates or updates the Device identified by
	// in.InstallationID. Fields left empty ("") in in are left unchanged on
	// an existing device (they are only ever cleared by DeleteDevice); the
	// platform is always overwritten. The returned Device's LoginIDs is not
	// populated (upserting a device does not load its links) — a caller
	// that needs them should follow up with GetDevice.
	UpsertDevice(ctx context.Context, in DeviceInput) (Device, error)

	// LinkLogin links the device identified by installationID to loginID
	// (idempotent: already being linked is not an error). Returns
	// ErrNotFound if no such device exists.
	LinkLogin(ctx context.Context, installationID, loginID string) error

	// UnlinkLogin removes the link between the device identified by
	// installationID and loginID, if any. It is not an error if the device
	// or the link does not exist.
	UnlinkLogin(ctx context.Context, installationID, loginID string) error

	// UnlinkLoginEverywhere removes loginID's link from every device it is
	// linked to and returns how many devices were affected. The devices
	// themselves are not deleted.
	UnlinkLoginEverywhere(ctx context.Context, loginID string) (int, error)

	// DeleteDevice deletes the device identified by installationID, and
	// every login link it had (cascade). It is not an error if the device
	// does not exist.
	DeleteDevice(ctx context.Context, installationID string) error

	// GetDevice returns the device identified by installationID, with
	// LoginIDs populated (sorted). Returns ErrNotFound if it does not exist.
	GetDevice(ctx context.Context, installationID string) (Device, error)

	// FindDevicesByLogins returns one DeviceMatch per device that is linked
	// to at least one of loginIDs, ordered by device ID ascending, with
	// each DeviceMatch.LoginIDs holding the (sorted) subset of loginIDs that
	// device is linked to.
	FindDevicesByLogins(ctx context.Context, loginIDs []string) ([]DeviceMatch, error)

	// FindDevicesByInstallationIDs returns the devices whose installation ID
	// is in installationIDs, ordered by device ID ascending, with LoginIDs
	// populated (sorted; nil if the device has no login links). Unknown
	// installation IDs are skipped and empty input returns nil, nil.
	FindDevicesByInstallationIDs(ctx context.Context, installationIDs []string) ([]Device, error)

	// ListDevices returns every device matching filter, ordered by device ID
	// ascending, with LoginIDs populated like FindDevicesByInstallationIDs.
	// A non-empty filter.Platforms keeps devices whose platform is one of
	// them; a non-empty filter.LocalePrefixes keeps devices whose locale
	// starts with any of the prefixes, case-insensitively. That is a plain
	// string prefix match (no BCP 47 tag boundary: "ja" also matches "jav"),
	// so it is only a pre-filter — Service.Send re-applies MatchesFilter for
	// the exact rule. Devices with no locale never match a locale prefix.
	ListDevices(ctx context.Context, filter DeviceFilter) ([]Device, error)
}

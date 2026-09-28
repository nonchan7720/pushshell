// Package core is the domain layer of this backend: the Device/DeviceLogin
// model, the Store abstraction persistence lives behind, and Service (the
// use cases the HTTP layer calls into). It is stdlib-only on purpose (no
// ent, no internal/api, no net/http) so that both cmd/server (ent-backed
// internal/store/entstore) and cmd/worker (database/sql-backed
// internal/store/sqlstore, no ent/atlas/kin-openapi in its build) can share
// it unchanged.
package core

import "time"

// Platform is the set of client platforms a Device can register as.
type Platform string

// Supported platforms (mirrors the OpenAPI Platform enum, ../../../openapi/openapi.yaml).
const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

// Valid reports whether p is a known Platform value.
func (p Platform) Valid() bool {
	switch p {
	case PlatformIOS, PlatformAndroid:
		return true
	default:
		return false
	}
}

// Device is a push notification destination: one app installation
// (installation_id, unique). Which login IDs it is linked to is carried in
// LoginIDs (sorted), stored separately by the Store (see DeviceLogin in
// internal/ent/schema for the underlying many-to-many relationship: one
// device can have several accounts logged in, one account can have several
// devices).
type Device struct {
	ID             int64
	InstallationID string
	// Platform is a Platform value ("ios" | "android"), kept as a plain
	// string here so callers that only pass it through (push.Message.Platform
	// is a string, too) do not need to convert back and forth.
	Platform    string
	PushToken   string
	DeviceToken string
	AppID       string
	AppVersion  string
	BuildNumber string
	OSVersion   string
	DeviceModel string
	Locale      string
	// LoginIDs are the login IDs this device is linked to, sorted. Left nil
	// by Store methods that do not need to load it (e.g. UpsertDevice).
	LoginIDs  []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DeviceInput is the input to Service.Register / Store.UpsertDevice: the
// registration fields from DeviceRegistration in ../../../openapi/openapi.yaml.
// Optional fields use "" for "not provided", matching how the previous
// internal/handler treated a nil *string — this lets UpsertDevice tell "the
// client did not send this field, keep the existing value" apart from
// "keep the existing value" without needing pointers.
type DeviceInput struct {
	LoginID        string
	InstallationID string
	// Platform is a Platform value ("ios" | "android") as a string; see
	// validate.go for how it is checked.
	Platform    string
	PushToken   string
	DeviceToken string
	AppID       string
	AppVersion  string
	BuildNumber string
	OSVersion   string
	DeviceModel string
	Locale      string
}

// DeviceMatch is one entry of Store.FindDevicesByLogins: a Device plus the
// subset of the requested login IDs (sorted) that are linked to it. Used by
// Service.Send to dedupe: a device linked to several of the requested login
// IDs still gets exactly one push.Message, and its DeliveryResult.LoginIDs
// lists every matched login ID.
type DeviceMatch struct {
	Device   Device
	LoginIDs []string
}

// SendInput is the input to Service.Send: the fields of
// SendNotificationRequest in ../../../openapi/openapi.yaml, already
// unwrapped from their optional-pointer JSON representation (see validate.go
// for the constraints checked on them).
type SendInput struct {
	LoginIDs  []string
	Title     string
	Body      string
	URL       string
	Data      map[string]any
	Badge     *int
	Sound     string
	ChannelID string
}

// DeliveryResult is the outcome of sending one push.Message to one device
// (SendNotificationRequest can target several login IDs that share a
// device; see DeviceMatch).
type DeliveryResult struct {
	InstallationID string
	LoginIDs       []string
	// Status is "ok" | "error".
	Status       string
	Error        string
	Unregistered bool
}

// SendResult is the return value of Service.Send.
type SendResult struct {
	Requested int
	Sent      int
	Failed    int
	Results   []DeliveryResult
}

package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/nonchan7720/pushshell/backend/internal/push"
)

// ErrUnauthorized is returned by Service methods (and Authorizer
// implementations) when the caller is not allowed to perform a device
// registration/unregistration. The transport layer
// (internal/transport/httpapi) maps it to an HTTP 401.
var ErrUnauthorized = errors.New("unauthorized")

// Authorizer authorizes device registration/unregistration
// (Service.Register, Service.UnregisterDevice, Service.UnregisterDeviceLogin).
//
// The web app can pass any token it likes when it registers a device (the
// bridge's `token`, sent as Authorization: Bearer). The app forwards it
// as-is, so implementing this lets a deployment verify it (e.g. against its
// own session store) and stop a third party from registering a device
// under someone else's login ID. The default AllowAll performs no check.
type Authorizer interface {
	// AuthorizeDevice returns nil if token is valid for loginID (loginID is
	// "" for operations that are not scoped to one login ID, e.g.
	// UnregisterDevice). It should return ErrUnauthorized (or an error
	// wrapping it) to deny the request.
	AuthorizeDevice(ctx context.Context, loginID, token string) error
}

// AllowAll is an Authorizer that allows every request.
type AllowAll struct{}

// AuthorizeDevice implements Authorizer.
func (AllowAll) AuthorizeDevice(context.Context, string, string) error { return nil }

// Service implements the use cases behind the OpenAPI operations
// (../../../openapi/openapi.yaml): registering/unregistering devices and
// login links, and sending notifications. It is the single place that
// combines validation (validate.go), authorization (Authorizer) and
// persistence (Store) — internal/transport/httpapi only translates between
// api.* (JSON) types and this package's, and maps the typed errors below to
// HTTP status codes.
type Service struct {
	store      Store
	sender     push.Sender
	authorizer Authorizer
	logger     *slog.Logger
}

// New builds a Service. authorizer/logger may be nil (AllowAll / slog.Default()).
func New(store Store, sender push.Sender, authorizer Authorizer, logger *slog.Logger) *Service {
	if authorizer == nil {
		authorizer = AllowAll{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, sender: sender, authorizer: authorizer, logger: logger}
}

// Register upserts the device described by in (keyed by in.InstallationID)
// and links it to in.LoginID (many-to-many — an existing link is left
// alone, a new one is added; device info/tokens are updated either way).
// bearer is the Authorization: Bearer token, if any (see Authorizer).
func (s *Service) Register(ctx context.Context, in DeviceInput, bearer string) (Device, error) {
	if err := validateDeviceInput(in); err != nil {
		return Device{}, err
	}
	if err := s.authorize(ctx, in.LoginID, bearer); err != nil {
		return Device{}, err
	}

	if _, err := s.store.UpsertDevice(ctx, in); err != nil {
		return Device{}, fmt.Errorf("core: upsert device: %w", err)
	}
	if err := s.store.LinkLogin(ctx, in.InstallationID, in.LoginID); err != nil {
		return Device{}, fmt.Errorf("core: link login: %w", err)
	}
	d, err := s.store.GetDevice(ctx, in.InstallationID)
	if err != nil {
		return Device{}, fmt.Errorf("core: load device: %w", err)
	}

	s.logger.Info("device registered",
		"installationId", d.InstallationID, "loginId", in.LoginID, "platform", d.Platform)
	return d, nil
}

// UnregisterDevice deletes the device identified by installationID
// entirely, cascading every login link it had. It is a no-op (no error) if
// the device does not exist — in that case bearer is not even checked,
// matching the previous internal/handler behavior.
func (s *Service) UnregisterDevice(ctx context.Context, installationID, bearer string) error {
	if err := validateInstallationID(installationID); err != nil {
		return err
	}
	if _, err := s.store.GetDevice(ctx, installationID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("core: load device: %w", err)
	}
	if err := s.authorize(ctx, "", bearer); err != nil {
		return err
	}
	if err := s.store.DeleteDevice(ctx, installationID); err != nil {
		return fmt.Errorf("core: delete device: %w", err)
	}
	s.logger.Info("device unregistered", "installationId", installationID)
	return nil
}

// UnregisterDeviceLogin removes only the link between the device identified
// by installationID and loginID; the device itself, its tokens and its
// other login links are untouched. It is a no-op (no error, no bearer
// check) if the device does not exist.
func (s *Service) UnregisterDeviceLogin(ctx context.Context, installationID, loginID, bearer string) error {
	if err := validateInstallationID(installationID); err != nil {
		return err
	}
	if err := validateLoginID(loginID); err != nil {
		return err
	}
	if _, err := s.store.GetDevice(ctx, installationID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("core: load device: %w", err)
	}
	if err := s.authorize(ctx, loginID, bearer); err != nil {
		return err
	}
	if err := s.store.UnlinkLogin(ctx, installationID, loginID); err != nil {
		return fmt.Errorf("core: unlink login: %w", err)
	}
	s.logger.Info("device login unlinked", "installationId", installationID, "loginId", loginID)
	return nil
}

// UnregisterLogin unlinks loginID from every device (server-to-server,
// apiKeyAuth only — no Authorizer/bearer involved). It returns how many
// devices were affected; the devices themselves are not deleted.
func (s *Service) UnregisterLogin(ctx context.Context, loginID string) (int, error) {
	if err := validateLoginID(loginID); err != nil {
		return 0, err
	}
	n, err := s.store.UnlinkLoginEverywhere(ctx, loginID)
	if err != nil {
		return 0, fmt.Errorf("core: unlink login everywhere: %w", err)
	}
	s.logger.Info("login unregistered from all devices", "loginId", loginID, "removed", n)
	return n, nil
}

// GetDevice returns the device identified by installationID (with LoginIDs
// populated). Returns ErrNotFound if it does not exist.
func (s *Service) GetDevice(ctx context.Context, installationID string) (Device, error) {
	if err := validateInstallationID(installationID); err != nil {
		return Device{}, err
	}
	d, err := s.store.GetDevice(ctx, installationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Device{}, err
		}
		return Device{}, fmt.Errorf("core: load device: %w", err)
	}
	return d, nil
}

// sendTarget is one device Service.Send will push to, plus the requested
// login IDs (SendInput.LoginIDs) it was reached through (empty if it was
// only reached via InstallationIDs or Broadcast).
type sendTarget struct {
	device   Device
	loginIDs []string
}

// resolveTargets turns the targeting fields of in into the set of devices to
// push to: FindDevicesByLogins for LoginIDs, FindDevicesByInstallationIDs for
// InstallationIDs (union, one entry per device), or ListDevices for
// Broadcast; then in.Filter is applied exactly (MatchesFilter). The result is
// ordered by device ID ascending.
func (s *Service) resolveTargets(ctx context.Context, in SendInput) ([]sendTarget, error) {
	byID := map[int64]*sendTarget{}
	add := func(d Device, loginIDs []string) {
		t, ok := byID[d.ID]
		if !ok {
			t = &sendTarget{device: d}
			byID[d.ID] = t
		}
		t.loginIDs = append(t.loginIDs, loginIDs...)
	}

	if len(in.LoginIDs) > 0 {
		matches, err := s.store.FindDevicesByLogins(ctx, in.LoginIDs)
		if err != nil {
			return nil, fmt.Errorf("core: find devices by logins: %w", err)
		}
		for _, m := range matches {
			add(m.Device, m.LoginIDs)
		}
	}
	if len(in.InstallationIDs) > 0 {
		devices, err := s.store.FindDevicesByInstallationIDs(ctx, in.InstallationIDs)
		if err != nil {
			return nil, fmt.Errorf("core: find devices by installation ids: %w", err)
		}
		for _, d := range devices {
			add(d, nil)
		}
	}
	if in.Broadcast {
		devices, err := s.store.ListDevices(ctx, in.Filter)
		if err != nil {
			return nil, fmt.Errorf("core: list devices: %w", err)
		}
		for _, d := range devices {
			add(d, nil)
		}
	}

	targets := make([]sendTarget, 0, len(byID))
	for _, t := range byID {
		if !MatchesFilter(t.device, in.Filter) {
			continue
		}
		targets = append(targets, *t)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].device.ID < targets[j].device.ID })
	return targets, nil
}

// Send delivers a notification to the devices selected by in.LoginIDs (every
// device linked to any of them), in.InstallationIDs (those devices directly)
// or in.Broadcast (every device), narrowed by in.Filter. A device reached
// several ways (e.g. linked to several of the requested login IDs, or also
// listed in InstallationIDs) still gets exactly one push.Message, and results
// are ordered by device ID ascending; a device whose send result comes back
// Unregistered is deleted.
func (s *Service) Send(ctx context.Context, in SendInput) (SendResult, error) {
	if err := validateSendInput(in); err != nil {
		return SendResult{}, err
	}

	matches, err := s.resolveTargets(ctx, in)
	if err != nil {
		return SendResult{}, err
	}

	result := SendResult{Requested: len(matches), Results: make([]DeliveryResult, 0, len(matches))}
	if len(matches) == 0 {
		return result, nil
	}

	data := map[string]any{}
	for k, v := range in.Data {
		data[k] = v
	}
	if in.URL != "" {
		data["url"] = in.URL
	}

	messages := make([]push.Message, len(matches))
	for i, m := range matches {
		messages[i] = push.Message{
			Platform:          m.device.Platform,
			ExpoToken:         m.device.PushToken,
			DeviceToken:       m.device.DeviceToken,
			Title:             in.Title,
			Body:              in.Body,
			Data:              data,
			Sound:             in.Sound,
			Badge:             in.Badge,
			ChannelID:         in.ChannelID,
			TTLSeconds:        in.TTLSeconds,
			Priority:          in.Priority,
			CollapseKey:       in.CollapseKey,
			Image:             in.Image,
			Silent:            in.Silent,
			Subtitle:          in.Subtitle,
			ThreadID:          in.ThreadID,
			InterruptionLevel: in.InterruptionLevel,
		}
	}

	sent, err := s.sender.Send(ctx, messages)
	if err != nil {
		return SendResult{}, fmt.Errorf("core: send push: %w", err)
	}
	if len(sent) != len(matches) {
		return SendResult{}, fmt.Errorf("core: send push: result count mismatch: got %d want %d", len(sent), len(matches))
	}

	for i, m := range matches {
		r := sent[i]
		dr := DeliveryResult{InstallationID: m.device.InstallationID, LoginIDs: m.loginIDs, Status: "ok"}
		if r.OK {
			result.Sent++
		} else {
			result.Failed++
			dr.Status = "error"
			dr.Error = r.Error
			if r.Unregistered {
				if err := s.store.DeleteDevice(ctx, m.device.InstallationID); err != nil {
					s.logger.Warn("failed to delete unregistered device", "installationId", m.device.InstallationID, "err", err)
				} else {
					dr.Unregistered = true
					s.logger.Info("device removed (token unregistered)", "installationId", m.device.InstallationID)
				}
			}
		}
		result.Results = append(result.Results, dr)
	}
	s.logger.Info("notification sent",
		"loginIds", len(in.LoginIDs), "installationIds", len(in.InstallationIDs), "broadcast", in.Broadcast,
		"requested", result.Requested, "sent", result.Sent, "failed", result.Failed)
	return result, nil
}

// authorize calls s.authorizer.AuthorizeDevice and normalizes its error:
// ErrUnauthorized (or a wrapped one) is returned as-is, anything else is
// wrapped as an unexpected error.
func (s *Service) authorize(ctx context.Context, loginID, bearer string) error {
	if err := s.authorizer.AuthorizeDevice(ctx, loginID, bearer); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return err
		}
		return fmt.Errorf("core: authorize device: %w", err)
	}
	return nil
}

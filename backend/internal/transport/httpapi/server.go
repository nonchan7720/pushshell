package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/nonchan7720/pushshell/backend/internal/api"
	"github.com/nonchan7720/pushshell/backend/internal/core"
)

// server implements api.StrictServerInterface on top of a *core.Service,
// translating between api.* (JSON) types and core.*'s, and mapping the
// typed errors core.Service returns to the documented HTTP responses
// (400/401/404) — anything else is returned as a plain error, which New's
// ResponseErrorHandlerFunc turns into a 500.
type server struct {
	svc *core.Service
}

var _ api.StrictServerInterface = (*server)(nil)

// Healthz implements api.StrictServerInterface.
func (s *server) Healthz(context.Context, api.HealthzRequestObject) (api.HealthzResponseObject, error) {
	return api.Healthz200JSONResponse{Status: "ok"}, nil
}

// RegisterDevice implements api.StrictServerInterface.
func (s *server) RegisterDevice(ctx context.Context, req api.RegisterDeviceRequestObject) (api.RegisterDeviceResponseObject, error) {
	if req.Body == nil {
		return api.RegisterDevice400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
			Code: "invalid_request", Message: "request body is required",
		}}, nil
	}
	in := core.DeviceInput{
		LoginID:        req.Body.LoginId,
		InstallationID: req.Body.InstallationId,
		Platform:       string(req.Body.Platform),
		PushToken:      fromPtr(req.Body.PushToken),
		DeviceToken:    fromPtr(req.Body.DeviceToken),
		AppID:          fromPtr(req.Body.AppId),
		AppVersion:     fromPtr(req.Body.AppVersion),
		BuildNumber:    fromPtr(req.Body.BuildNumber),
		OSVersion:      fromPtr(req.Body.OsVersion),
		DeviceModel:    fromPtr(req.Body.DeviceModel),
		Locale:         fromPtr(req.Body.Locale),
	}

	d, err := s.svc.Register(ctx, in, BearerFromContext(ctx))
	if err != nil {
		var inv core.ErrInvalidInput
		switch {
		case errors.As(err, &inv):
			return api.RegisterDevice400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
				Code: "invalid_request", Message: inv.Message,
			}}, nil
		case errors.Is(err, core.ErrUnauthorized):
			return api.RegisterDevice401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
				Code: "unauthorized", Message: err.Error(),
			}}, nil
		default:
			return nil, fmt.Errorf("register device: %w", err)
		}
	}
	return api.RegisterDevice200JSONResponse(toAPIDevice(d)), nil
}

// UnregisterDevice implements api.StrictServerInterface.
func (s *server) UnregisterDevice(ctx context.Context, req api.UnregisterDeviceRequestObject) (api.UnregisterDeviceResponseObject, error) {
	err := s.svc.UnregisterDevice(ctx, req.InstallationId, BearerFromContext(ctx))
	if err != nil {
		if errors.Is(err, core.ErrUnauthorized) {
			return api.UnregisterDevice401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
				Code: "unauthorized", Message: err.Error(),
			}}, nil
		}
		var inv core.ErrInvalidInput
		if errors.As(err, &inv) {
			// installationId comes from the URL path (already length-checked
			// by the route/OpenAPI validator), so this is not expected in
			// practice, but map it to 400 instead of a 500 just in case.
			return nil, inv
		}
		return nil, fmt.Errorf("unregister device: %w", err)
	}
	return api.UnregisterDevice204Response{}, nil
}

// UnregisterDeviceLogin implements api.StrictServerInterface.
func (s *server) UnregisterDeviceLogin(ctx context.Context, req api.UnregisterDeviceLoginRequestObject) (api.UnregisterDeviceLoginResponseObject, error) {
	err := s.svc.UnregisterDeviceLogin(ctx, req.InstallationId, req.LoginId, BearerFromContext(ctx))
	if err != nil {
		if errors.Is(err, core.ErrUnauthorized) {
			return api.UnregisterDeviceLogin401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
				Code: "unauthorized", Message: err.Error(),
			}}, nil
		}
		var inv core.ErrInvalidInput
		if errors.As(err, &inv) {
			return nil, inv
		}
		return nil, fmt.Errorf("unregister device login: %w", err)
	}
	return api.UnregisterDeviceLogin204Response{}, nil
}

// UnregisterLogin implements api.StrictServerInterface.
func (s *server) UnregisterLogin(ctx context.Context, req api.UnregisterLoginRequestObject) (api.UnregisterLoginResponseObject, error) {
	n, err := s.svc.UnregisterLogin(ctx, req.LoginId)
	if err != nil {
		var inv core.ErrInvalidInput
		if errors.As(err, &inv) {
			return nil, inv
		}
		return nil, fmt.Errorf("unregister login: %w", err)
	}
	return api.UnregisterLogin200JSONResponse{Removed: n}, nil
}

// GetDevice implements api.StrictServerInterface.
func (s *server) GetDevice(ctx context.Context, req api.GetDeviceRequestObject) (api.GetDeviceResponseObject, error) {
	d, err := s.svc.GetDevice(ctx, req.InstallationId)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return api.GetDevice404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
				Code: "not_found", Message: "device not found",
			}}, nil
		}
		var inv core.ErrInvalidInput
		if errors.As(err, &inv) {
			return nil, inv
		}
		return nil, fmt.Errorf("get device: %w", err)
	}
	return api.GetDevice200JSONResponse(toAPIDevice(d)), nil
}

// SendNotification implements api.StrictServerInterface.
func (s *server) SendNotification(ctx context.Context, req api.SendNotificationRequestObject) (api.SendNotificationResponseObject, error) {
	if req.Body == nil {
		return api.SendNotification400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
			Code: "invalid_request", Message: "request body is required",
		}}, nil
	}
	body := req.Body
	in := core.SendInput{
		LoginIDs:        derefSlice(body.LoginIds),
		InstallationIDs: derefSlice(body.InstallationIds),
		Broadcast:       body.Broadcast != nil && *body.Broadcast,
		Title:           fromPtr(body.Title),
		Body:            fromPtr(body.Body),
		URL:             fromPtr(body.Url),
		Sound:           fromPtr(body.Sound),
		ChannelID:       fromPtr(body.ChannelId),
		Badge:           body.Badge,

		TTLSeconds:  body.Ttl,
		CollapseKey: fromPtr(body.CollapseKey),
		Image:       fromPtr(body.Image),
		Silent:      body.Silent != nil && *body.Silent,
		Subtitle:    fromPtr(body.Subtitle),
		ThreadID:    fromPtr(body.ThreadId),
	}
	if body.Priority != nil {
		in.Priority = string(*body.Priority)
	}
	if body.InterruptionLevel != nil {
		in.InterruptionLevel = string(*body.InterruptionLevel)
	}
	if body.Filter != nil {
		if body.Filter.Platforms != nil {
			// A non-nil (possibly empty) slice tells core the field was set, so
			// it can reject e.g. "platforms": [] like the OpenAPI minItems does.
			in.Filter.Platforms = make([]string, len(*body.Filter.Platforms))
			for i, p := range *body.Filter.Platforms {
				in.Filter.Platforms[i] = string(p)
			}
		}
		if body.Filter.Locales != nil {
			in.Filter.LocalePrefixes = append([]string{}, *body.Filter.Locales...)
		}
	}
	if body.Data != nil {
		in.Data = *body.Data
	}

	res, err := s.svc.Send(ctx, in)
	if err != nil {
		var inv core.ErrInvalidInput
		if errors.As(err, &inv) {
			return api.SendNotification400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
				Code: "invalid_request", Message: inv.Message,
			}}, nil
		}
		return nil, fmt.Errorf("send notification: %w", err)
	}
	return api.SendNotification200JSONResponse(toAPISendResult(res)), nil
}

func toAPIDevice(d core.Device) api.Device {
	loginIDs := d.LoginIDs
	if loginIDs == nil {
		loginIDs = []string{}
	}
	return api.Device{
		Id:             d.ID,
		InstallationId: d.InstallationID,
		LoginIds:       loginIDs,
		Platform:       api.Platform(d.Platform),
		PushToken:      toPtr(d.PushToken),
		DeviceToken:    toPtr(d.DeviceToken),
		AppId:          toPtr(d.AppID),
		AppVersion:     toPtr(d.AppVersion),
		BuildNumber:    toPtr(d.BuildNumber),
		OsVersion:      toPtr(d.OSVersion),
		DeviceModel:    toPtr(d.DeviceModel),
		Locale:         toPtr(d.Locale),
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
}

func toAPISendResult(r core.SendResult) api.SendNotificationResult {
	results := make([]api.DeliveryResult, len(r.Results))
	for i, dr := range r.Results {
		loginIDs := dr.LoginIDs
		if loginIDs == nil {
			loginIDs = []string{}
		}
		out := api.DeliveryResult{
			InstallationId: dr.InstallationID,
			LoginIds:       loginIDs,
			Status:         api.DeliveryResultStatus(dr.Status),
		}
		if dr.Error != "" {
			out.Error = toPtr(dr.Error)
		}
		if dr.Unregistered {
			out.Unregistered = &dr.Unregistered
		}
		results[i] = out
	}
	return api.SendNotificationResult{
		Requested: r.Requested,
		Sent:      r.Sent,
		Failed:    r.Failed,
		Results:   results,
	}
}

func derefSlice[T any](s *[]T) []T {
	if s == nil {
		return nil
	}
	return *s
}

func fromPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

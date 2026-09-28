// Package handler は OpenAPI で定義された API の実装。
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nonchan7720/webapp-notification/backend/internal/api"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent/device"
	"github.com/nonchan7720/webapp-notification/backend/internal/push"
)

// ErrUnauthorized は Authorizer が認可を拒否したことを表す。
var ErrUnauthorized = errors.New("unauthorized")

// Handler は api.StrictServerInterface の実装。
type Handler struct {
	db         *ent.Client
	sender     push.Sender
	authorizer Authorizer
	logger     *slog.Logger
}

var _ api.StrictServerInterface = (*Handler)(nil)

// New は Handler を作る。authorizer / logger は nil 可。
func New(db *ent.Client, sender push.Sender, authorizer Authorizer, logger *slog.Logger) *Handler {
	if authorizer == nil {
		authorizer = AllowAll{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{db: db, sender: sender, authorizer: authorizer, logger: logger}
}

// Healthz implements api.StrictServerInterface.
func (h *Handler) Healthz(context.Context, api.HealthzRequestObject) (api.HealthzResponseObject, error) {
	return api.Healthz200JSONResponse{Status: "ok"}, nil
}

// RegisterDevice implements api.StrictServerInterface.
func (h *Handler) RegisterDevice(ctx context.Context, req api.RegisterDeviceRequestObject) (api.RegisterDeviceResponseObject, error) {
	body := req.Body
	if body == nil {
		return api.RegisterDevice400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
			Code: "invalid_request", Message: "request body is required",
		}}, nil
	}
	if isEmptyOrNil(body.PushToken) && isEmptyOrNil(body.DeviceToken) {
		return api.RegisterDevice400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
			Code: "invalid_request", Message: "at least one of pushToken or deviceToken is required",
		}}, nil
	}
	if err := h.authorizer.AuthorizeDevice(ctx, body.LoginId, bearerFromContext(ctx)); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return api.RegisterDevice401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
				Code: "unauthorized", Message: err.Error(),
			}}, nil
		}
		return nil, fmt.Errorf("authorize device: %w", err)
	}

	err := h.db.Device.Create().
		SetInstallationID(body.InstallationId).
		SetLoginID(body.LoginId).
		SetPlatform(device.Platform(body.Platform)).
		SetNillablePushToken(body.PushToken).
		SetNillableDeviceToken(body.DeviceToken).
		SetNillableAppID(body.AppId).
		SetNillableAppVersion(body.AppVersion).
		SetNillableBuildNumber(body.BuildNumber).
		SetNillableOsVersion(body.OsVersion).
		SetNillableDeviceModel(body.DeviceModel).
		SetNillableLocale(body.Locale).
		OnConflictColumns(device.FieldInstallationID).
		UpdateNewValues().
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert device: %w", err)
	}

	d, err := h.db.Device.Query().Where(device.InstallationID(body.InstallationId)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}
	h.logger.Info("device registered",
		"installationId", d.InstallationID, "loginId", d.LoginID, "platform", d.Platform)
	return api.RegisterDevice200JSONResponse(toAPIDevice(d)), nil
}

// UnregisterDevice implements api.StrictServerInterface.
func (h *Handler) UnregisterDevice(ctx context.Context, req api.UnregisterDeviceRequestObject) (api.UnregisterDeviceResponseObject, error) {
	d, err := h.db.Device.Query().Where(device.InstallationID(req.InstallationId)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return api.UnregisterDevice204Response{}, nil
		}
		return nil, fmt.Errorf("load device: %w", err)
	}
	if err := h.authorizer.AuthorizeDevice(ctx, d.LoginID, bearerFromContext(ctx)); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return api.UnregisterDevice401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse{
				Code: "unauthorized", Message: err.Error(),
			}}, nil
		}
		return nil, fmt.Errorf("authorize device: %w", err)
	}
	if err := h.db.Device.DeleteOne(d).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("delete device: %w", err)
	}
	h.logger.Info("device unregistered", "installationId", d.InstallationID, "loginId", d.LoginID)
	return api.UnregisterDevice204Response{}, nil
}

// GetDevice implements api.StrictServerInterface.
func (h *Handler) GetDevice(ctx context.Context, req api.GetDeviceRequestObject) (api.GetDeviceResponseObject, error) {
	d, err := h.db.Device.Query().Where(device.InstallationID(req.InstallationId)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return api.GetDevice404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse{
				Code: "not_found", Message: "device not found",
			}}, nil
		}
		return nil, fmt.Errorf("load device: %w", err)
	}
	return api.GetDevice200JSONResponse(toAPIDevice(d)), nil
}

// SendNotification implements api.StrictServerInterface.
func (h *Handler) SendNotification(ctx context.Context, req api.SendNotificationRequestObject) (api.SendNotificationResponseObject, error) {
	body := req.Body
	if body == nil || len(body.LoginIds) == 0 {
		return api.SendNotification400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse{
			Code: "invalid_request", Message: "loginIds is required",
		}}, nil
	}

	devices, err := h.db.Device.Query().
		Where(device.LoginIDIn(body.LoginIds...)).
		Order(ent.Asc(device.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query devices: %w", err)
	}

	result := api.SendNotificationResult{
		Requested: len(devices),
		Results:   make([]api.DeliveryResult, 0, len(devices)),
	}
	if len(devices) == 0 {
		return api.SendNotification200JSONResponse(result), nil
	}

	data := map[string]any{}
	if body.Data != nil {
		for k, v := range *body.Data {
			data[k] = v
		}
	}
	if body.Url != nil && *body.Url != "" {
		data["url"] = *body.Url
	}

	messages := make([]push.Message, len(devices))
	for i, d := range devices {
		m := push.Message{
			Platform:    string(d.Platform),
			ExpoToken:   d.PushToken,
			DeviceToken: d.DeviceToken,
			Title:       body.Title,
			Data:        data,
			Badge:       body.Badge,
		}
		if body.Body != nil {
			m.Body = *body.Body
		}
		if body.Sound != nil {
			m.Sound = *body.Sound
		}
		if body.ChannelId != nil {
			m.ChannelID = *body.ChannelId
		}
		messages[i] = m
	}

	sent, err := h.sender.Send(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("send push: %w", err)
	}
	if len(sent) != len(devices) {
		return nil, fmt.Errorf("send push: result count mismatch: got %d want %d", len(sent), len(devices))
	}

	for i, d := range devices {
		r := sent[i]
		dr := api.DeliveryResult{
			InstallationId: d.InstallationID,
			LoginId:        d.LoginID,
			Status:         api.DeliveryResultStatusOk,
		}
		if r.OK {
			result.Sent++
		} else {
			result.Failed++
			dr.Status = api.DeliveryResultStatusError
			errMsg := r.Error
			dr.Error = &errMsg
			if r.Unregistered {
				if err := h.db.Device.DeleteOne(d).Exec(ctx); err != nil && !ent.IsNotFound(err) {
					h.logger.Warn("failed to delete unregistered device", "installationId", d.InstallationID, "err", err)
				} else {
					unregistered := true
					dr.Unregistered = &unregistered
					h.logger.Info("device removed (token unregistered)", "installationId", d.InstallationID)
				}
			}
		}
		result.Results = append(result.Results, dr)
	}
	h.logger.Info("notification sent",
		"loginIds", len(body.LoginIds), "requested", result.Requested, "sent", result.Sent, "failed", result.Failed)
	return api.SendNotification200JSONResponse(result), nil
}

func toAPIDevice(d *ent.Device) api.Device {
	return api.Device{
		Id:             int64(d.ID),
		InstallationId: d.InstallationID,
		LoginId:        d.LoginID,
		Platform:       api.Platform(d.Platform),
		PushToken:      optString(d.PushToken),
		DeviceToken:    optString(d.DeviceToken),
		AppId:          optString(d.AppID),
		AppVersion:     optString(d.AppVersion),
		BuildNumber:    optString(d.BuildNumber),
		OsVersion:      optString(d.OsVersion),
		DeviceModel:    optString(d.DeviceModel),
		Locale:         optString(d.Locale),
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
}

// isEmptyOrNil は *string が nil または空文字列かを返す。
func isEmptyOrNil(s *string) bool {
	return s == nil || *s == ""
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type ctxKey int

const bearerKey ctxKey = iota

// WithBearer は Authorization: Bearer のトークンをコンテキストに載せる。
func WithBearer(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerKey, token)
}

func bearerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(bearerKey).(string)
	return v
}

// BearerFromRequest は Authorization ヘッダから Bearer トークンを取り出す。
func BearerFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

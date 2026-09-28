package core

import (
	"fmt"
	"net/url"
)

// Field length limits, mirroring ../../../openapi/openapi.yaml
// (DeviceRegistration / DeviceInfo / SendNotificationRequest schemas).
const (
	maxLoginIDLen        = 256
	maxInstallationIDLen = 128
	maxPushTokenLen      = 512
	maxDeviceTokenLen    = 4096
	maxAppIDLen          = 256
	maxAppVersionLen     = 64
	maxBuildNumberLen    = 64
	maxOSVersionLen      = 64
	maxDeviceModelLen    = 128
	maxLocaleLen         = 32

	maxSendLoginIDs = 1000
	maxTitleLen     = 256
	maxBodyLen      = 4096
)

// ErrInvalidInput is returned by Service methods (and the validate*
// functions below) when the caller's input fails validation. The transport
// layer (internal/transport/httpapi) maps it to an HTTP 400.
type ErrInvalidInput struct {
	Message string
}

func (e ErrInvalidInput) Error() string { return e.Message }

func invalidf(format string, args ...any) error {
	return ErrInvalidInput{Message: fmt.Sprintf(format, args...)}
}

// validateDeviceInput checks in against the DeviceRegistration constraints
// in ../../../openapi/openapi.yaml.
func validateDeviceInput(in DeviceInput) error {
	if err := validateLoginID(in.LoginID); err != nil {
		return err
	}
	if err := validateInstallationID(in.InstallationID); err != nil {
		return err
	}
	if !Platform(in.Platform).Valid() {
		return invalidf("platform must be one of %q, %q; got %q", PlatformIOS, PlatformAndroid, in.Platform)
	}
	if in.PushToken == "" && in.DeviceToken == "" {
		return invalidf("at least one of pushToken or deviceToken is required")
	}
	if err := maxLen("pushToken", in.PushToken, maxPushTokenLen); err != nil {
		return err
	}
	if err := maxLen("deviceToken", in.DeviceToken, maxDeviceTokenLen); err != nil {
		return err
	}
	if err := maxLen("appId", in.AppID, maxAppIDLen); err != nil {
		return err
	}
	if err := maxLen("appVersion", in.AppVersion, maxAppVersionLen); err != nil {
		return err
	}
	if err := maxLen("buildNumber", in.BuildNumber, maxBuildNumberLen); err != nil {
		return err
	}
	if err := maxLen("osVersion", in.OSVersion, maxOSVersionLen); err != nil {
		return err
	}
	if err := maxLen("deviceModel", in.DeviceModel, maxDeviceModelLen); err != nil {
		return err
	}
	if err := maxLen("locale", in.Locale, maxLocaleLen); err != nil {
		return err
	}
	return nil
}

// validateInstallationID checks a bare installationId (path parameter or
// DeviceInput field) against the InstallationId constraints in
// ../../../openapi/openapi.yaml.
func validateInstallationID(id string) error {
	if id == "" {
		return invalidf("installationId is required")
	}
	return maxLen("installationId", id, maxInstallationIDLen)
}

// validateLoginID checks a bare loginId (path parameter or DeviceInput
// field) against the loginId constraints in ../../../openapi/openapi.yaml.
func validateLoginID(id string) error {
	if id == "" {
		return invalidf("loginId is required")
	}
	return maxLen("loginId", id, maxLoginIDLen)
}

// validateSendInput checks in against the SendNotificationRequest
// constraints in ../../../openapi/openapi.yaml.
func validateSendInput(in SendInput) error {
	if len(in.LoginIDs) == 0 {
		return invalidf("loginIds is required")
	}
	if len(in.LoginIDs) > maxSendLoginIDs {
		return invalidf("loginIds must have at most %d items", maxSendLoginIDs)
	}
	for _, id := range in.LoginIDs {
		if id == "" {
			return invalidf("loginIds must not contain empty strings")
		}
	}
	if in.Title == "" {
		return invalidf("title is required")
	}
	if err := maxLen("title", in.Title, maxTitleLen); err != nil {
		return err
	}
	if err := maxLen("body", in.Body, maxBodyLen); err != nil {
		return err
	}
	if in.URL != "" {
		u, err := url.Parse(in.URL)
		if err != nil || !u.IsAbs() {
			return invalidf("url must be an absolute URL")
		}
	}
	return nil
}

func maxLen(field, value string, limit int) error {
	if len(value) > limit {
		return invalidf("%s must be at most %d characters", field, limit)
	}
	return nil
}

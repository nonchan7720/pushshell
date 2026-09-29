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

	maxSendLoginIDs        = 1000
	maxSendInstallationIDs = 1000
	maxTitleLen            = 256
	maxBodyLen             = 4096

	maxFilterPlatforms = 2
	maxFilterLocales   = 50
	maxFilterLocaleLen = 32

	maxTTLSeconds     = 28 * 24 * 60 * 60 // 28 days, the FCM limit
	maxCollapseKeyLen = 64
	maxImageURLLen    = 2048
	maxSubtitleLen    = 256
	maxThreadIDLen    = 64
)

// Priority values accepted by SendInput.Priority ("" means the default, high).
const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
)

// InterruptionLevel values accepted by SendInput.InterruptionLevel.
const (
	InterruptionPassive       = "passive"
	InterruptionActive        = "active"
	InterruptionTimeSensitive = "time-sensitive"
	InterruptionCritical      = "critical"
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
	if err := validateSendTargets(in); err != nil {
		return err
	}
	if err := validateDeviceFilter(in.Filter); err != nil {
		return err
	}

	if in.Silent {
		if in.Title != "" || in.Body != "" || in.Sound != "" || in.Badge != nil || in.Subtitle != "" || in.Image != "" {
			return invalidf("silent notifications carry data only: title, body, sound, badge, subtitle and image must not be set")
		}
	} else if in.Title == "" {
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
	return validateSendOptions(in)
}

// validateSendTargets checks the targeting fields of in: at least one of
// loginIds / installationIds / broadcast, and broadcast on its own.
func validateSendTargets(in SendInput) error {
	if len(in.LoginIDs) == 0 && len(in.InstallationIDs) == 0 && !in.Broadcast {
		return invalidf("at least one of loginIds, installationIds or broadcast is required")
	}
	if in.Broadcast && (len(in.LoginIDs) > 0 || len(in.InstallationIDs) > 0) {
		return invalidf("broadcast cannot be combined with loginIds or installationIds")
	}
	if len(in.LoginIDs) > maxSendLoginIDs {
		return invalidf("loginIds must have at most %d items", maxSendLoginIDs)
	}
	for _, id := range in.LoginIDs {
		if id == "" {
			return invalidf("loginIds must not contain empty strings")
		}
	}
	if len(in.InstallationIDs) > maxSendInstallationIDs {
		return invalidf("installationIds must have at most %d items", maxSendInstallationIDs)
	}
	for _, id := range in.InstallationIDs {
		if id == "" {
			return invalidf("installationIds must not contain empty strings")
		}
		if len(id) > maxInstallationIDLen {
			return invalidf("installationIds items must be at most %d characters", maxInstallationIDLen)
		}
	}
	return nil
}

// validateDeviceFilter checks a SendInput.Filter. A nil slice means "not
// set"; a non-nil empty one is rejected, like minItems: 1 in the OpenAPI schema.
func validateDeviceFilter(f DeviceFilter) error {
	if f.Platforms != nil {
		if len(f.Platforms) == 0 || len(f.Platforms) > maxFilterPlatforms {
			return invalidf("filter.platforms must have 1 to %d items", maxFilterPlatforms)
		}
		for _, p := range f.Platforms {
			if !Platform(p).Valid() {
				return invalidf("filter.platforms must contain only %q or %q; got %q", PlatformIOS, PlatformAndroid, p)
			}
		}
	}
	if f.LocalePrefixes != nil {
		if len(f.LocalePrefixes) == 0 || len(f.LocalePrefixes) > maxFilterLocales {
			return invalidf("filter.locales must have 1 to %d items", maxFilterLocales)
		}
		for _, l := range f.LocalePrefixes {
			if l == "" {
				return invalidf("filter.locales must not contain empty strings")
			}
			if len(l) > maxFilterLocaleLen {
				return invalidf("filter.locales items must be at most %d characters", maxFilterLocaleLen)
			}
		}
	}
	return nil
}

// validateSendOptions checks the delivery option fields of in (ttl,
// priority, collapseKey, image, subtitle, threadId, interruptionLevel).
func validateSendOptions(in SendInput) error {
	if in.TTLSeconds != nil && (*in.TTLSeconds < 0 || *in.TTLSeconds > maxTTLSeconds) {
		return invalidf("ttl must be between 0 and %d seconds", maxTTLSeconds)
	}
	switch in.Priority {
	case "", PriorityHigh, PriorityNormal:
	default:
		return invalidf("priority must be %q or %q; got %q", PriorityHigh, PriorityNormal, in.Priority)
	}
	if err := maxLen("collapseKey", in.CollapseKey, maxCollapseKeyLen); err != nil {
		return err
	}
	if in.Image != "" {
		if len(in.Image) > maxImageURLLen {
			return invalidf("image must be at most %d characters", maxImageURLLen)
		}
		u, err := url.Parse(in.Image)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return invalidf("image must be an absolute http(s) URL")
		}
	}
	if err := maxLen("subtitle", in.Subtitle, maxSubtitleLen); err != nil {
		return err
	}
	if err := maxLen("threadId", in.ThreadID, maxThreadIDLen); err != nil {
		return err
	}
	switch in.InterruptionLevel {
	case "", InterruptionPassive, InterruptionActive, InterruptionTimeSensitive, InterruptionCritical:
	default:
		return invalidf("interruptionLevel must be one of %q, %q, %q, %q; got %q",
			InterruptionPassive, InterruptionActive, InterruptionTimeSensitive, InterruptionCritical, in.InterruptionLevel)
	}
	return nil
}

func maxLen(field, value string, limit int) error {
	if len(value) > limit {
		return invalidf("%s must be at most %d characters", field, limit)
	}
	return nil
}

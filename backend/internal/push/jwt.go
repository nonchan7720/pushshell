package push

import (
	"encoding/base64"
	"encoding/json"
)

// jwtEncodeSegment base64url-encodes (no padding) v as a JWT header/claims
// segment, per RFC 7519. Shared by fcm.go (RS256, service-account JWT
// bearer) and apns.go (ES256, APNs provider token) so neither needs
// golang.org/x/oauth2, github.com/golang-jwt/jwt, or github.com/sideshow/apns2
// just to build a two-segment signing input.
func jwtEncodeSegment(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

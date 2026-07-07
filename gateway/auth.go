package gateway

import (
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func BuildAuth(provider string, credential map[string]any) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{Provider: provider, Metadata: credential}
}

// NeedsRefresh reports whether the access token is missing an expiry or is past
// it. ExpirationTime reads Metadata["expired"] (and expire/expires_at/...).
func NeedsRefresh(a *cliproxyauth.Auth) bool {
	exp, ok := a.ExpirationTime()
	return !ok || time.Now().After(exp)
}

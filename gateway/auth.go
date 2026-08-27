package gateway

import (
	"strings"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// BuildAuth drops the envelope's credential into Metadata, and lifts base_url
// into Attributes as well: codexCreds reads the upstream address from
// Attributes and nowhere else, so a Proof-pool relay address supplied in the
// envelope reaches the executor only through this copy.
//
// Only base_url is lifted. api_key deliberately is not — the envelope carries
// OAuth tokens only, and an api_key attribute would reclassify the Auth as
// AuthKindAPIKey, flip codexAuthUsesAPIKey, and shadow the
// Metadata["access_token"] fallback every production credential depends on.
//
// Attributes stays nil when there is nothing to carry: a non-nil map is itself
// a signal that several executor branches test for.
func BuildAuth(provider string, credential map[string]any) *cliproxyauth.Auth {
	a := &cliproxyauth.Auth{Provider: provider, Metadata: credential}
	if v, ok := credential["base_url"].(string); ok {
		// Trimmed, because codexCreds is the one reader that does not trim: a
		// blank value there would shadow the compiled default rather than fall
		// through to it.
		if v = strings.TrimSpace(v); v != "" {
			a.Attributes = map[string]string{"base_url": v}
		}
	}
	return a
}

// NeedsRefresh reports whether the access token is missing an expiry or is past
// it. ExpirationTime reads Metadata["expired"] (and expire/expires_at/...).
func NeedsRefresh(a *cliproxyauth.Auth) bool {
	exp, ok := a.ExpirationTime()
	return !ok || time.Now().After(exp)
}

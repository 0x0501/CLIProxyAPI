package gateway

import (
	"testing"
	"time"
)

func TestBuildAuthPutsCredentialInMetadata(t *testing.T) {
	a := BuildAuth("codex", map[string]any{"access_token": "tok"})
	if a.Provider != "codex" || a.Metadata["access_token"] != "tok" {
		t.Fatalf("bad auth: %+v", a)
	}
	if a.Attributes != nil {
		t.Fatal("OAuth credential must not populate Attributes")
	}
}

func TestBuildAuthLiftsBaseURLIntoAttributes(t *testing.T) {
	a := BuildAuth("codex", map[string]any{"access_token": "tok", "base_url": "https://relay.example/v1"})
	if a.Attributes["base_url"] != "https://relay.example/v1" {
		t.Fatalf("base_url did not reach Attributes: %+v", a.Attributes)
	}
	// Metadata keeps everything it carried before, base_url included.
	if a.Metadata["access_token"] != "tok" || a.Metadata["base_url"] != "https://relay.example/v1" {
		t.Fatalf("metadata lost a key: %+v", a.Metadata)
	}
	// api_key is never lifted: it would reclassify the Auth as AuthKindAPIKey
	// and shadow the Metadata["access_token"] fallback.
	if _, ok := a.Attributes["api_key"]; ok {
		t.Fatal("api_key must not be lifted into Attributes")
	}
}

func TestBuildAuthLeavesAttributesNilWithoutBaseURL(t *testing.T) {
	// The executor default upstream must survive every credential outside the
	// Proof pool — that is every credential in production today.
	for name, credential := range map[string]map[string]any{
		"absent":     {"access_token": "tok"},
		"empty":      {"access_token": "tok", "base_url": ""},
		"whitespace": {"access_token": "tok", "base_url": "   "},
		"non-string": {"access_token": "tok", "base_url": 42},
		"nil":        {"access_token": "tok", "base_url": nil},
	} {
		if a := BuildAuth("codex", credential); a.Attributes != nil {
			t.Fatalf("%s base_url populated Attributes: %+v", name, a.Attributes)
		}
	}
}

func TestNeedsRefresh(t *testing.T) {
	past := BuildAuth("codex", map[string]any{"expired": time.Now().Add(-time.Hour).Format(time.RFC3339)})
	future := BuildAuth("codex", map[string]any{"expired": time.Now().Add(time.Hour).Format(time.RFC3339)})
	missing := BuildAuth("codex", map[string]any{"access_token": "tok"})
	if !NeedsRefresh(past) {
		t.Fatal("expired token should need refresh")
	}
	if NeedsRefresh(future) {
		t.Fatal("valid token should not need refresh")
	}
	if !NeedsRefresh(missing) {
		t.Fatal("missing expiry should need refresh")
	}
}

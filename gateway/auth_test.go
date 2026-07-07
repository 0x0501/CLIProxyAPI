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

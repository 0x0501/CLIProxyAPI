package gateway

import (
	"context"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestUsageRequestCodexURLAndHeaders(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{
			"access_token": "codex-tok",
			"account_id":   "acct-1",
		},
	}
	req, err := usageRequest(context.Background(), "codex", auth)
	if err != nil {
		t.Fatalf("usageRequest: %v", err)
	}
	if req.Method != "GET" {
		t.Fatalf("method = %q", req.Method)
	}
	if got := req.URL.String(); got != "https://chatgpt.com/backend-api/wham/usage" {
		t.Fatalf("url = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer codex-tok" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct-1" {
		t.Fatalf("ChatGPT-Account-Id = %q", got)
	}
}

func TestUsageRequestXaiBillingCreditsURLAndHeaders(t *testing.T) {
	// Grok Build CLI: GET cli-chat-proxy /v1/billing?format=credits with the
	// same OAuth identity headers as OAuth inference (ADR 0028).
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{
			"access_token": "xai-tok",
			"account_id":   "oidc-sub-1",
		},
	}
	req, err := usageRequest(context.Background(), "xai", auth)
	if err != nil {
		t.Fatalf("usageRequest: %v", err)
	}
	if req.Method != "GET" {
		t.Fatalf("method = %q", req.Method)
	}
	if got := req.URL.String(); got != "https://cli-chat-proxy.grok.com/v1/billing?format=credits" {
		t.Fatalf("url = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer xai-tok" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("X-XAI-Token-Auth"); got != "xai-grok-cli" {
		t.Fatalf("X-XAI-Token-Auth = %q", got)
	}
	if got := req.Header.Get("x-userid"); got != "oidc-sub-1" {
		t.Fatalf("x-userid = %q", got)
	}
	if got := req.Header.Get("x-grok-client-version"); got != "0.2.120" {
		t.Fatalf("x-grok-client-version = %q", got)
	}
}

func TestUsageRequestXaiRequiresAccessTokenAndAccountID(t *testing.T) {
	if _, err := usageRequest(context.Background(), "xai", &cliproxyauth.Auth{
		Metadata: map[string]any{"account_id": "sub"},
	}); err == nil {
		t.Fatal("expected missing access token error")
	}
	if _, err := usageRequest(context.Background(), "xai", &cliproxyauth.Auth{
		Metadata: map[string]any{"access_token": "tok"},
	}); err == nil {
		t.Fatal("expected missing account id error")
	}
}

func TestUsageRequestUnsupportedProvider(t *testing.T) {
	if _, err := usageRequest(context.Background(), "claude", &cliproxyauth.Auth{
		Metadata: map[string]any{"access_token": "tok"},
	}); err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

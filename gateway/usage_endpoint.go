package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// usageEnvelope is the worker->gateway /usage request body: just provider +
// credential (no chat payload). The credential is dropped into auth.Metadata
// exactly like /invoke, so refresh and token lookup behave identically.
type usageEnvelope struct {
	Provider   string         `json:"provider"`
	Credential map[string]any `json:"credential"`
}

// usageRequest builds the provider-specific account-usage GET: a lightweight
// authenticated request that returns quota utilization WITHOUT a chat request.
// Adding claude/gemini = one more case (e.g. GET api.anthropic.com/api/oauth/usage
// with `anthropic-beta: oauth-2025-04-20`).
func usageRequest(ctx context.Context, provider string, auth *cliproxyauth.Auth) (*http.Request, error) {
	switch provider {
	case "codex":
		access, _ := auth.Metadata["access_token"].(string)
		if access == "" {
			return nil, errors.New("usage: missing access token")
		}
		// ChatGPT-login usage endpoint (== {base_url}/api/codex/usage for the API base).
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+access)
		if acct, _ := auth.Metadata["account_id"].(string); acct != "" {
			req.Header.Set("ChatGPT-Account-Id", acct)
		}
		req.Header.Set("User-Agent", "codex_cli_rs/0.44.0")
		return req, nil
	default:
		return nil, errors.New("usage: unsupported provider " + provider)
	}
}

// UsageHandler fetches per-account quota/usage from the provider's usage endpoint
// and returns the raw upstream {status_code, body} to the worker. It goes through
// the uTLS Chrome-fingerprint client because Cloudflare blocks the plain CF-egress
// TLS fingerprint on chatgpt.com/api.anthropic.com (a deployed Worker fetch gets a
// 403 challenge; the uTLS client passes). Refreshes the token inline like /invoke
// and echoes the rotated credential via X-Tokenswim-Refreshed.
func UsageHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var env usageEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if env.Provider == "" || len(env.Credential) == 0 {
			http.Error(w, "usage: missing provider or credential", http.StatusBadRequest)
			return
		}
		provider, ok := Lookup(env.Provider)
		if !ok {
			http.Error(w, "unknown provider: "+env.Provider, http.StatusBadRequest)
			return
		}

		auth := BuildAuth(env.Provider, env.Credential)
		exec := provider.NewExecutor(cfg)

		if NeedsRefresh(auth) {
			refreshed, rerr := exec.Refresh(r.Context(), auth)
			if rerr != nil {
				writePreStreamError(w, ClassifyRefreshError(rerr))
				return
			}
			if refreshed != nil {
				auth = refreshed
			}
			// Hand the rotated tokens back so the worker persists them (same as /invoke).
			if hdr, herr := json.Marshal(auth.Metadata); herr == nil {
				w.Header().Set("X-Tokenswim-Refreshed", string(hdr))
			}
		}

		req, err := usageRequest(r.Context(), env.Provider, auth)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// A discrete request/response (not a streamed inference connection), so a
		// timeout is allowed — same rationale as the management APICall timeout.
		client := helps.NewUtlsHTTPClient(r.Context(), cfg, auth, 30*time.Second)
		resp, derr := client.Do(req)
		if derr != nil {
			writePreStreamError(w, ClassifyExecError(derr))
			return
		}
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil {
				_ = cerr
			}
		}()
		body, _ := io.ReadAll(resp.Body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": resp.StatusCode,
			"body":        string(body),
		})
	}
}

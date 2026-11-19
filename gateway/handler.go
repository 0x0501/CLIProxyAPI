package gateway

import (
	"net/http"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// requestWantsStream reports whether the client request body asked for SSE streaming.
func requestWantsStream(payload []byte) bool {
	return ojson.GetBytes(payload, "stream").Bool()
}

func Handler(cfg *config.Config, sessions *UpstreamSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		env, err := DecodeEnvelope(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		provider, ok := Lookup(env.Provider)
		if !ok {
			http.Error(w, "unknown provider: "+env.Provider, http.StatusBadRequest)
			return
		}
		serve(w, r, cfg, sessions, provider, env, BuildAuth(env.Provider, env.Credential))
	}
}

func serve(w http.ResponseWriter, r *http.Request, cfg *config.Config, sessions *UpstreamSessions, provider Provider, env Envelope, auth *cliproxyauth.Auth) {
	var exec cliproxyauth.ProviderExecutor = provider.NewExecutor(cfg)
	var metadata map[string]any
	ctx := r.Context()
	if env.Session != "" && env.Provider == "codex" && sessions != nil {
		// One upstream socket per named session; the executor serialises
		// calls on it and keeps it open between them.
		exec = sessions.exec
		metadata = map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: env.Session}
		defer sessions.Begin(env.Session)()
		if env.Continuation {
			// No new dial and no HTTP fallback: either the live socket that
			// holds previous_response_id carries it, or the call fails as
			// session_lost and the caller resends the full input.
			ctx = cliproxyexecutor.WithRequiredUpstreamWebsocket(ctx)
		}
		w.Header().Set(UpstreamSessionHeader, "1")
	} else if env.Continuation {
		http.Error(w, "continuation requires a codex session", http.StatusBadRequest)
		return
	}

	if NeedsRefresh(auth) {
		refreshed, rerr := exec.Refresh(ctx, auth)
		if rerr != nil {
			writePreStreamError(w, ClassifyRefreshError(rerr))
			return
		}
		if refreshed != nil {
			auth = refreshed
		}
		// Hand refreshed tokens back to the worker BEFORE the body starts.
		if hdr, herr := json.Marshal(auth.Metadata); herr == nil {
			w.Header().Set("X-Tokenswim-Refreshed", string(hdr))
		}
	}

	var modelHint struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(env.Request, &modelHint)

	// The capture slot must be in the context the executor runs under: the
	// usage plugin correlates raw upstream detail to this request by that
	// context and nothing else (ADR 0019).
	ctx, capture := NewUsageCapture(ctx, modelHint.Model)

	profile := LookupFormat(env.Format)
	format := sdktranslator.FromString(env.Format)
	execReq := cliproxyexecutor.Request{Model: modelHint.Model, Payload: env.Request}
	opts := cliproxyexecutor.Options{
		Stream:          requestWantsStream(env.Request),
		SourceFormat:    format,
		ResponseFormat:  format,
		OriginalRequest: env.Request,
		Metadata:        metadata,
	}

	if !opts.Stream {
		resp, xerr := exec.Execute(ctx, auth, execReq, opts)
		if xerr != nil {
			writePreStreamError(w, errorWithTiming(ClassifyExecError(xerr), capture))
			return
		}
		reparsed, haveReparsed := profile.ParseUsage(resp.Payload)
		if u, ok := ResolveUsage(capture, reparsed, haveReparsed, modelHint.Model); ok {
			if b, err := json.Marshal(u); err == nil {
				w.Header().Set("X-Tokenswim-Usage", string(b))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp.Payload)
		return
	}

	result, xerr := exec.ExecuteStream(ctx, auth, execReq, opts)
	if xerr != nil {
		writePreStreamError(w, errorWithTiming(ClassifyExecError(xerr), capture))
		return
	}
	chunks := result.Chunks
	if metadata != nil {
		// The WebSocket executor reports a failure on the upstream's first event
		// as a queued stream error, not as an ExecuteStream error. Surfacing it
		// before the status line keeps failover and the session_lost resend.
		var berr error
		if chunks, berr = splitBootstrapError(chunks); berr != nil {
			writePreStreamError(w, errorWithTiming(ClassifyExecError(berr), capture))
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	PipeStream(w, flush, chunks, profile, modelHint.Model, capture)
}

func writePreStreamError(w http.ResponseWriter, p ErrorPayload) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(p.HTTPStatus())
	_ = json.NewEncoder(w).Encode(map[string]any{"error": p})
}

package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func Handler(cfg *config.Config) http.HandlerFunc {
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
			// Hand refreshed tokens back to the worker BEFORE the body starts.
			if hdr, herr := json.Marshal(auth.Metadata); herr == nil {
				w.Header().Set("X-Tokenswim-Refreshed", string(hdr))
			}
		}

		var modelHint struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(env.Request, &modelHint)
		format := sdktranslator.FromString(env.Provider)
		result, xerr := exec.ExecuteStream(r.Context(), auth, cliproxyexecutor.Request{
			Model:   modelHint.Model,
			Payload: env.Request,
		}, cliproxyexecutor.Options{
			Stream:          true,
			SourceFormat:    format,
			ResponseFormat:  format,
			OriginalRequest: env.Request,
		})
		if xerr != nil {
			writePreStreamError(w, ClassifyExecError(xerr))
			return
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
		PipeStream(w, flush, result.Chunks, provider, modelHint.Model)
	}
}

func writePreStreamError(w http.ResponseWriter, p ErrorPayload) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(p.HTTPStatus())
	_ = json.NewEncoder(w).Encode(map[string]any{"error": p})
}

package gateway

import (
	"errors"
	"io"

	"github.com/MoeForge/moego/json"
)

// Envelope is the worker->gateway request body: an opaque credential map dropped
// straight into auth.Metadata, plus a raw native /responses payload.
type Envelope struct {
	Provider   string          `json:"provider"`
	Credential map[string]any  `json:"credential"`
	Request    json.RawMessage `json:"request"`
	Format     string          `json:"format"`
	// Session names an upstream Responses WebSocket kept open across calls
	// (one per Consumer socket and credential). Empty means a one-shot HTTP call.
	Session string `json:"session"`
	// Continuation marks a request carrying previous_response_id plus only the
	// new items. It is valid only on the session's live upstream socket and must
	// never be replayed over HTTP, which would drop the earlier context.
	Continuation bool `json:"continuation"`
}

func DecodeEnvelope(r io.Reader) (Envelope, error) {
	var env Envelope
	if err := json.NewDecoder(r).Decode(&env); err != nil {
		return Envelope{}, err
	}
	if env.Provider == "" {
		return Envelope{}, errors.New("envelope: missing provider")
	}
	if len(env.Credential) == 0 {
		return Envelope{}, errors.New("envelope: missing credential")
	}
	if len(env.Request) == 0 {
		return Envelope{}, errors.New("envelope: missing request")
	}
	if env.Continuation && env.Session == "" {
		return Envelope{}, errors.New("envelope: continuation requires session")
	}
	if env.Format == "" {
		env.Format = "codex"
	}
	return env, nil
}

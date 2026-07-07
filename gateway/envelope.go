package gateway

import (
	"encoding/json"
	"errors"
	"io"
)

// Envelope is the worker->gateway request body: an opaque credential map dropped
// straight into auth.Metadata, plus a raw native /responses payload.
type Envelope struct {
	Provider   string          `json:"provider"`
	Credential map[string]any  `json:"credential"`
	Request    json.RawMessage `json:"request"`
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
	return env, nil
}

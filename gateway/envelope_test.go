package gateway

import (
	"strings"
	"testing"
)

func TestDecodeEnvelopeOK(t *testing.T) {
	body := `{"provider":"codex","credential":{"access_token":"a"},"request":{"model":"gpt-5-codex","input":[]}}`
	env, err := DecodeEnvelope(strings.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Provider != "codex" || env.Credential["access_token"] != "a" {
		t.Fatalf("bad decode: %+v", env)
	}
	if len(env.Request) == 0 {
		t.Fatal("request payload must be preserved raw")
	}
}

func TestDecodeEnvelopeRejectsMissingFields(t *testing.T) {
	for _, body := range []string{
		`{"credential":{"access_token":"a"},"request":{}}`,       // no provider
		`{"provider":"codex","request":{}}`,                      // no credential
		`{"provider":"codex","credential":{"access_token":"a"}}`, // no request
	} {
		if _, err := DecodeEnvelope(strings.NewReader(body)); err == nil {
			t.Fatalf("expected error for body %s", body)
		}
	}
}

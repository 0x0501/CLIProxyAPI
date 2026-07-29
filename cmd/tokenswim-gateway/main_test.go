package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

// The Tokenswim Worker probes every registered gateway node on /healthz and
// only routes traffic to one that answers with this exact handshake (protocol
// 1). Anything else — a different service, an older image, a bare 200 — is
// treated as unhealthy, so this response is a wire contract, not a nicety.
func TestHealthzAnswersProtocol1Handshake(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(&config.Config{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got struct {
		Status   string `json:"status"`
		Service  string `json:"service"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if got.Status != "ok" || got.Service != "tokenswim-gateway" || got.Protocol != 1 {
		t.Fatalf("handshake = %+v, want {ok tokenswim-gateway 1}", got)
	}
}

func TestHealthzAnswersHead(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(&config.Config{}).ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

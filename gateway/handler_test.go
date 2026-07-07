package gateway

// NOTE: full happy-path exercising the real CodexExecutor requires a live
// upstream; here we assert the handler's request-shaping and error-path
// behavior. The real executor is covered by the manual e2e in Task 14.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestHandlerRejectsBadEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	Handler(&config.Config{})(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestHandlerRejectsUnknownProvider(t *testing.T) {
	rec := httptest.NewRecorder()
	body := `{"provider":"nope","credential":{"access_token":"a"},"request":{"model":"m"}}`
	Handler(&config.Config{})(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown provider, got %d", rec.Code)
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayRequiresAuthenticatedWebCaller(t *testing.T) {
	secret := strings.Repeat("s", 32)
	handler := authenticateGateway(secret, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, test := range []struct {
		token  string
		status int
	}{{"", http.StatusForbidden}, {strings.Repeat("x", 32), http.StatusForbidden}, {secret, http.StatusNoContent}} {
		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		request.Header.Set("X-Tokenswim-Gateway-Secret", test.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("status = %d, want %d", response.Code, test.status)
		}
	}
}

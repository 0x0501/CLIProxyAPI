// Command tokenswim-gateway is a stateless HTTP gateway: each request carries a
// per-request OAuth credential + a native provider payload; it runs the matching
// executor and streams SSE back with trailing usage/error control frames.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/gateway"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func main() {
	logging.SetupBaseLogger()
	cfg := &config.Config{}
	// PROXY_URL routes all upstream traffic (incl. the uTLS/websocket paths
	// that ignore HTTP_PROXY) through a forward proxy. Used by local dev on
	// networks where the sandbox DNS/egress cannot reach providers directly.
	if proxyURL := strings.TrimSpace(os.Getenv("PROXY_URL")); proxyURL != "" {
		cfg.ProxyURL = proxyURL
		log.Printf("tokenswim-gateway: upstream proxy %s", proxyutil.Redact(proxyURL))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/invoke", gateway.Handler(cfg))
	mux.HandleFunc("/models", gateway.ModelsHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	addr := ":8787"
	log.Printf("tokenswim-gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("tokenswim-gateway: server error: %v", err)
		os.Exit(1)
	}
}

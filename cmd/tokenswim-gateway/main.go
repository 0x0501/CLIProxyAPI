// Command tokenswim-gateway is a stateless HTTP gateway: each request carries a
// per-request OAuth credential + a native provider payload; it runs the matching
// executor and streams SSE back with trailing usage/error control frames.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/gateway"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

// gatewayProtocol is the version of the Tokenswim gateway wire contract this
// binary speaks. The Worker refuses to route to a node reporting anything else,
// so bump it only alongside a coordinated image rollout.
const gatewayProtocol = 1

// newMux wires the gateway routes. Split out of main so the /healthz handshake
// can be tested without binding a port.
func newMux(cfg *config.Config) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/invoke", gateway.Handler(cfg))
	mux.HandleFunc("/usage", gateway.UsageHandler(cfg))
	mux.HandleFunc("/models", gateway.ModelsHandler)
	mux.HandleFunc("/healthz", healthz)
	return mux
}

// healthz identifies the service and its protocol version so a Tokenswim health
// probe can tell this gateway apart from any other HTTP listener on the node.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"service":  "tokenswim-gateway",
		"protocol": gatewayProtocol,
	}); err != nil {
		log.Printf("tokenswim-gateway: healthz write failed: %v", err)
	}
}

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
	mux := newMux(cfg)

	addr := ":8787"
	log.Printf("tokenswim-gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("tokenswim-gateway: server error: %v", err)
		os.Exit(1)
	}
}

// Command tokenswim-gateway is a stateless HTTP gateway: each request carries a
// per-request OAuth credential + a native provider payload; it runs the matching
// executor and streams SSE back with trailing usage/error control frames.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/router-for-me/CLIProxyAPI/v7/gateway"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
)

func main() {
	logging.SetupBaseLogger()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", gateway.Handler(&config.Config{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	addr := ":8787"
	log.Printf("tokenswim-gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("tokenswim-gateway: server error: %v", err)
		os.Exit(1)
	}
}

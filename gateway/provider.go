package gateway

import (
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	codexexec "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// Provider bundles everything that varies per upstream provider: how to build
// its executor. Adding grok/gemini = add one Registry entry; the handler,
// streaming, and error paths stay provider-agnostic.
type Provider struct {
	NewExecutor func(*config.Config) cliproxyauth.ProviderExecutor
}

var Registry = map[string]Provider{
	"codex": {
		NewExecutor: func(cfg *config.Config) cliproxyauth.ProviderExecutor {
			return codexexec.NewCodexExecutor(cfg)
		},
	},
}

func Lookup(name string) (Provider, bool) {
	p, ok := Registry[name]
	return p, ok
}

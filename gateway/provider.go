package gateway

import (
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	codexexec "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

// Provider bundles everything that varies per upstream provider: how to build
// its executor, how to read usage from its completion frame, and how to
// recognize that frame. Adding grok/gemini = add one Registry entry; the
// handler, streaming, and error paths stay provider-agnostic.
type Provider struct {
	NewExecutor func(*config.Config) auth.ProviderExecutor
	ParseUsage  func([]byte) (usage.Detail, bool)
	IsCompleted func([]byte) bool
}

var Registry = map[string]Provider{
	"codex": {
		NewExecutor: func(cfg *config.Config) auth.ProviderExecutor {
			return codexexec.NewCodexExecutor(cfg)
		},
		ParseUsage: helps.ParseCodexUsage,
		IsCompleted: func(frame []byte) bool {
			return gjson.GetBytes(frame, "type").String() == "response.completed"
		},
	},
}

func Lookup(name string) (Provider, bool) {
	p, ok := Registry[name]
	return p, ok
}

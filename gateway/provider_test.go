package gateway

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestLookupCodexRegistered(t *testing.T) {
	p, ok := Lookup("codex")
	if !ok {
		t.Fatal("codex must be registered")
	}
	if p.NewExecutor == nil {
		t.Fatal("codex provider must define executor factory")
	}
	if exec := p.NewExecutor(newTestConfig()); exec == nil {
		t.Fatal("NewExecutor returned nil")
	}
}

func TestLookupUnknownProvider(t *testing.T) {
	if _, ok := Lookup("does-not-exist"); ok {
		t.Fatal("unknown provider must not resolve")
	}
}

// Test helper
func newTestConfig() *config.Config { return &config.Config{} }

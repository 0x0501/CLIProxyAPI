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
	if p.NewExecutor == nil || p.ParseUsage == nil || p.IsCompleted == nil {
		t.Fatal("codex provider must define executor factory, usage parser, and completed predicate")
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

func TestIsCompletedDetectsCompletedEvent(t *testing.T) {
	p, _ := Lookup("codex")
	if !p.IsCompleted([]byte(`{"type":"response.completed","response":{"usage":{"total_tokens":5}}}`)) {
		t.Fatal("must detect response.completed frame")
	}
	if p.IsCompleted([]byte(`{"type":"response.output_text.delta"}`)) {
		t.Fatal("must not flag a delta frame")
	}
}

// Test helper
func newTestConfig() *config.Config { return &config.Config{} }

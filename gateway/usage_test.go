package gateway

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestFormatUsageEvent(t *testing.T) {
	got := FormatUsageEvent(UsagePayload{InputTokens: 3, OutputTokens: 7, TotalTokens: 10, Model: "gpt-5-codex"})
	if !bytes.HasPrefix(got, []byte("event: tokenswim.usage\n")) {
		t.Fatalf("missing event line: %q", got)
	}
	if !bytes.Contains(got, []byte(`"total_tokens":10`)) || !bytes.Contains(got, []byte(`"model":"gpt-5-codex"`)) {
		t.Fatalf("bad data line: %q", got)
	}
	if !bytes.HasSuffix(got, []byte("\n\n")) {
		t.Fatalf("event must end with blank line: %q", got)
	}
}

func TestUsageFromDetailIsLossless(t *testing.T) {
	// Anthropic-style independent fixture from the spec: uncached 100, cache_read 40,
	// cache_write 10, output 30, reasoning 12 → billable output total 42.
	d := usage.EnsureTokenBreakdownForProvider(
		usage.Detail{
			InputTokens:         100,
			OutputTokens:        30,
			ReasoningTokens:     12,
			CacheReadTokens:     40,
			CacheCreationTokens: 10,
		},
		"anthropic",
		"",
	)
	u := UsageFromDetail(d, "claude-sonnet")
	if u.CacheCreationTokens != 10 {
		t.Fatalf("Cache write dropped: %+v", u)
	}
	if u.CacheReadTokens != 40 {
		t.Fatalf("Cache read dropped: %+v", u)
	}
	if !u.BreakdownComplete {
		t.Fatalf("expected complete breakdown, got %+v", u.TokenBreakdown)
	}
	if u.TokenBreakdown.Input.CacheWriteTokens != 10 ||
		u.TokenBreakdown.Input.CacheReadTokens != 40 ||
		u.TokenBreakdown.Input.UncachedTokens != 100 ||
		u.TokenBreakdown.Output.TotalTokens != 42 {
		t.Fatalf("Token breakdown not preserved: %+v", u.TokenBreakdown)
	}
	if u.ResponseServiceTier != d.ResponseServiceTier {
		t.Fatalf("service tier dropped")
	}
	if u.Model != "claude-sonnet" {
		t.Fatalf("model: %q", u.Model)
	}

	// Round-trip through JSON — Worker must see the same fields.
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"cache_creation_tokens":10`)) {
		t.Fatalf("JSON missing cache_creation_tokens: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"token_breakdown"`)) {
		t.Fatalf("JSON missing token_breakdown: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"breakdown_complete":true`)) {
		t.Fatalf("JSON missing breakdown_complete: %s", raw)
	}
}

func TestUsageFromDetailIncompleteIsNotComplete(t *testing.T) {
	d := usage.EnsureTokenBreakdownForProvider(
		usage.Detail{InputTokens: 100, OutputTokens: 30, ReasoningTokens: 12},
		"plugin-provider",
		"",
	)
	u := UsageFromDetail(d, "m")
	if u.BreakdownComplete {
		t.Fatalf("unclassified must not be complete: %+v", u.TokenBreakdown)
	}
}

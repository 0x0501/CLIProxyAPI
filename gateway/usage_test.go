package gateway

import (
	"bytes"
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

func TestUsageFromDetail(t *testing.T) {
	d := usage.Detail{InputTokens: 1, OutputTokens: 2, ReasoningTokens: 4, CachedTokens: 5, TotalTokens: 3}
	u := UsageFromDetail(d, "m")
	if u.InputTokens != 1 || u.OutputTokens != 2 || u.ReasoningTokens != 4 || u.CachedTokens != 5 || u.TotalTokens != 3 || u.Model != "m" {
		t.Fatalf("bad mapping: %+v", u)
	}
}

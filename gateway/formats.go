package gateway

import (
	"bytes"

	// Registers all codex<->openai request/response (stream+non-stream) translators.
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	"github.com/tidwall/gjson"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// FormatProfile describes how to frame, terminate, and read usage for one
// response format. Extract yields raw JSON from a chunk (for terminal/usage
// detection); Frame yields the wire bytes (adds "data: " for raw-JSON formats).
type FormatProfile struct {
	Extract    func([]byte) []byte
	Frame      func([]byte) []byte
	IsTerminal func([]byte) bool
	ParseUsage func([]byte) (usage.Detail, bool)
	AppendDone bool
}

func stripData(b []byte) []byte { return bytes.TrimPrefix(b, []byte("data: ")) }

func usageHasTokens(d usage.Detail) bool {
	return d.InputTokens > 0 || d.OutputTokens > 0 || d.TotalTokens > 0
}

// codexUsage reads usage from a codex/responses payload, tolerating both the
// nested `response.usage` shape (streaming frames, codex non-stream) and the
// top-level `usage` shape (the openai-response non-stream translator unwraps
// the envelope). Returns ok=false only when neither location has usage.
func codexUsage(raw []byte) (usage.Detail, bool) {
	direct, okDirect := helps.ParseCodexUsage(raw)
	if okDirect && usageHasTokens(direct) {
		return direct, true
	}
	// Re-nest so a top-level `usage` resolves under `response.usage`. Prefer this
	// over a token-less direct hit: ParseCodexUsage returns ok=true on a
	// service-tier-only match, which would otherwise shadow the real top-level
	// usage and settle every non-stream request at zero tokens.
	wrapped := append(append([]byte(`{"response":`), raw...), '}')
	if d, ok := helps.ParseCodexUsage(wrapped); ok && usageHasTokens(d) {
		return d, true
	}
	return direct, okDirect
}

// codexStyle: chunks are already "data: {...}"; terminal = response.completed;
// usage from response.usage. Used for "codex" and "openai-response".
var codexStyle = FormatProfile{
	Extract:    stripData,
	Frame:      func(b []byte) []byte { return b },
	IsTerminal: func(raw []byte) bool { return gjson.GetBytes(raw, "type").String() == "response.completed" },
	ParseUsage: codexUsage,
	AppendDone: false,
}

// openaiStyle: chunks are raw chat.completion.chunk JSON (no "data:"); the
// terminal frame carries a flat "usage" node; clients expect a "data: [DONE]".
var openaiStyle = FormatProfile{
	Extract:    func(b []byte) []byte { return b },
	Frame:      func(b []byte) []byte { return append([]byte("data: "), b...) },
	IsTerminal: func(raw []byte) bool { return gjson.GetBytes(raw, "usage").Exists() },
	ParseUsage: func(raw []byte) (usage.Detail, bool) {
		d := helps.ParseOpenAIUsage(raw)
		return d, d.TotalTokens > 0
	},
	AppendDone: true,
}

var formatProfiles = map[string]FormatProfile{
	"codex":           codexStyle,
	"openai-response": codexStyle,
	"openai":          openaiStyle,
}

// LookupFormat returns the profile for a format string, defaulting to codex-style.
func LookupFormat(format string) FormatProfile {
	if p, ok := formatProfiles[format]; ok {
		return p
	}
	return codexStyle
}

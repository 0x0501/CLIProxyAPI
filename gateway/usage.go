package gateway

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// UsagePayload is the JSON body of the trailing tokenswim.usage SSE event the
// worker parses to write an agent_request row.
type UsagePayload struct {
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	Model           string `json:"model"`
}

func UsageFromDetail(d usage.Detail, model string) UsagePayload {
	return UsagePayload{
		InputTokens:     d.InputTokens,
		OutputTokens:    d.OutputTokens,
		ReasoningTokens: d.ReasoningTokens,
		CachedTokens:    d.CachedTokens,
		TotalTokens:     d.TotalTokens,
		Model:           model,
	}
}

func FormatUsageEvent(u UsagePayload) []byte {
	data, _ := json.Marshal(u)
	out := append([]byte("event: tokenswim.usage\ndata: "), data...)
	return append(out, '\n', '\n')
}

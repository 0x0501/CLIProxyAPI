package gateway

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// UsagePayload is the lossless JSON body of the trailing tokenswim.usage SSE
// event (and the X-Tokenswim-Usage non-stream header). It forwards every field
// CLIProxyAPI already knows on Detail — no hand-maintained subset (ADR 0014).
type UsagePayload struct {
	InputTokens         int64                `json:"input_tokens"`
	OutputTokens        int64                `json:"output_tokens"`
	ReasoningTokens     int64                `json:"reasoning_tokens"`
	CachedTokens        int64                `json:"cached_tokens"`
	CacheReadTokens     int64                `json:"cache_read_tokens"`
	CacheCreationTokens int64                `json:"cache_creation_tokens"`
	TotalTokens         int64                `json:"total_tokens"`
	TokenBreakdown      usage.TokenBreakdown `json:"token_breakdown"`
	ResponseServiceTier string               `json:"response_service_tier,omitempty"`
	// Structural completeness: Valid() && Quality == complete.
	BreakdownComplete bool   `json:"breakdown_complete"`
	Model             string `json:"model"`
}

func UsageFromDetail(d usage.Detail, model string) UsagePayload {
	complete := d.TokenBreakdown.Valid() &&
		d.TokenBreakdown.Quality == usage.TokenAccountingQualityComplete
	return UsagePayload{
		InputTokens:         d.InputTokens,
		OutputTokens:        d.OutputTokens,
		ReasoningTokens:     d.ReasoningTokens,
		CachedTokens:        d.CachedTokens,
		CacheReadTokens:     d.CacheReadTokens,
		CacheCreationTokens: d.CacheCreationTokens,
		TotalTokens:         d.TotalTokens,
		TokenBreakdown:      d.TokenBreakdown,
		ResponseServiceTier: d.ResponseServiceTier,
		BreakdownComplete:   complete,
		Model:               model,
	}
}

func FormatUsageEvent(u UsagePayload) []byte {
	data, _ := json.Marshal(u)
	out := append([]byte("event: tokenswim.usage\ndata: "), data...)
	return append(out, '\n', '\n')
}

package gateway

import (
	"bytes"
	"context"
	"testing"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

// codexClaudeChunks runs the REAL codex->claude response translator over a canned
// codex SSE stream and returns the claude chunks exactly as the executor emits
// them (one []byte per TranslateStream output — see CodexExecutor.ExecuteStream,
// which does `out <- StreamChunk{Payload: chunks[i]}`). Driving the live
// translator is what makes the billing-gate assertions below fail loudly if a
// translator change ever moves usage off the message_delta frame.
func codexClaudeChunks(t *testing.T) [][]byte {
	t.Helper()
	ctx := context.Background()
	from := sdktranslator.FromString("codex")
	to := sdktranslator.FromString("claude")
	req := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	lines := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","status":"in_progress"},"output_index":0}`,
		`data: {"type":"response.content_part.added","part":{"type":"output_text"},"content_index":0,"output_index":0}`,
		`data: {"type":"response.output_text.delta","delta":"Hello","output_index":0}`,
		`data: {"type":"response.output_item.done","item":{"type":"message","status":"completed"},"output_index":0}`,
		`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":{"input_tokens":13,"output_tokens":7,"input_tokens_details":{"cached_tokens":5}}}}`,
	}
	var param any
	var chunks [][]byte
	for _, line := range lines {
		out := sdktranslator.TranslateStream(ctx, from, to, "gpt-5", req, req, []byte(line), &param)
		chunks = append(chunks, out...)
	}
	return chunks
}

// claudeDataPayload returns the first data: JSON of the given claude event type
// found across all chunks (a single chunk can bundle several event:/data: blocks).
func claudeDataPayload(chunks [][]byte, typ string) []byte {
	for _, c := range chunks {
		for _, line := range bytes.Split(c, []byte("\n")) {
			t := bytes.TrimSpace(line)
			if !bytes.HasPrefix(t, []byte("data:")) {
				continue
			}
			j := bytes.TrimSpace(t[len("data:"):])
			if gjson.GetBytes(j, "type").String() == typ {
				return j
			}
		}
	}
	return nil
}

// TestLookupFormatClaudeTerminalIsMessageDelta locks the load-bearing terminal
// choice: the CLIProxyAPI translator carries usage only on message_delta, so the
// claude profile must treat message_delta as terminal and nothing else (not
// message_stop, not the block events). A wrong choice settles every request at
// the reserved ceiling (over-bill) or at zero (free output).
func TestLookupFormatClaudeTerminalIsMessageDelta(t *testing.T) {
	chunks := codexClaudeChunks(t)
	p := LookupFormat("claude")
	if p.AppendDone {
		t.Fatal("claude must not append [DONE]")
	}

	md := claudeDataPayload(chunks, "message_delta")
	if md == nil {
		t.Fatal("translator produced no message_delta event")
	}
	if !p.IsTerminal(md) {
		t.Fatalf("message_delta must be terminal: %s", md)
	}
	for _, typ := range []string{
		"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_stop",
	} {
		payload := claudeDataPayload(chunks, typ)
		if payload == nil {
			t.Fatalf("translator produced no %s event", typ)
		}
		if p.IsTerminal(payload) {
			t.Fatalf("%s must not be terminal: %s", typ, payload)
		}
	}
}

// TestClaudeProfileParseUsageFromTerminalChunk drives Extract+ParseUsage exactly
// as PipeStream does — on the raw upstream chunk, which bundles content_block_stop
// + message_delta + message_stop. Extract must dig the message_delta out of that
// bundle and ParseUsage must read its consolidated top-level usage (input +
// output + cache_read). A non-usage event must report ok=false so a stray frame
// never settles a request at zero.
func TestClaudeProfileParseUsageFromTerminalChunk(t *testing.T) {
	chunks := codexClaudeChunks(t)
	p := LookupFormat("claude")

	var terminal []byte
	terminalCount := 0
	for _, c := range chunks {
		if p.IsTerminal(p.Extract(c)) {
			terminal = c
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Fatalf("want exactly one terminal chunk, got %d", terminalCount)
	}

	d, ok := p.ParseUsage(p.Extract(terminal))
	if !ok {
		t.Fatal("terminal message_delta must yield usage ok=true")
	}
	// Consolidated usage the translator placed on message_delta (input excludes
	// the cache_read split): input 13 - cached 5 = 8, output 7, cache_read 5.
	if d.InputTokens != 8 || d.OutputTokens != 7 || d.CacheReadTokens != 5 {
		t.Fatalf("consolidated usage wrong: in=%d out=%d cacheRead=%d (want 8/7/5)",
			d.InputTokens, d.OutputTokens, d.CacheReadTokens)
	}

	// A non-terminal event carries no top-level usage → must not settle.
	nonUsage := claudeDataPayload(chunks, "content_block_delta")
	if _, ok := p.ParseUsage(nonUsage); ok {
		t.Fatalf("content_block_delta must yield ok=false: %s", nonUsage)
	}
}

func TestLookupFormatOpenAIFramesRawJSON(t *testing.T) {
	p := LookupFormat("openai")
	if !p.AppendDone {
		t.Fatal("openai must append [DONE]")
	}
	framed := p.Frame([]byte(`{"object":"chat.completion.chunk"}`))
	if !bytes.HasPrefix(framed, []byte("data: ")) {
		t.Fatalf("openai chunk must get a data: prefix, got %q", framed)
	}
	if !p.IsTerminal([]byte(`{"usage":{"total_tokens":5}}`)) {
		t.Fatal("openai terminal is a frame carrying a usage node")
	}
	if d, ok := p.ParseUsage([]byte(`{"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)); !ok || d.TotalTokens != 5 {
		t.Fatalf("openai usage parse failed: %+v ok=%v", d, ok)
	}
}

func TestCodexProfileUsageBothNestings(t *testing.T) {
	p := LookupFormat("openai-response")
	// Streaming/codex shape: nested response.usage.
	if d, ok := p.ParseUsage([]byte(`{"response":{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`)); !ok || d.TotalTokens != 5 {
		t.Fatalf("nested usage should parse: %+v ok=%v", d, ok)
	}
	// openai-response NON-STREAM shape: top-level usage (envelope unwrapped).
	if d, ok := p.ParseUsage([]byte(`{"id":"resp_1","usage":{"input_tokens":4,"output_tokens":4,"total_tokens":8}}`)); !ok || d.TotalTokens != 8 {
		t.Fatalf("top-level usage should parse: %+v ok=%v", d, ok)
	}
	// Same shape WITH a top-level service_tier: the direct parse matches on the
	// tier alone (ok=true, zero tokens) and must not shadow the real usage —
	// else every non-stream request settles at zero cost.
	if d, ok := p.ParseUsage([]byte(`{"id":"resp_1","service_tier":"default","usage":{"input_tokens":13,"output_tokens":19,"total_tokens":32}}`)); !ok || d.TotalTokens != 32 {
		t.Fatalf("top-level usage with service_tier should parse: %+v ok=%v", d, ok)
	}
	// No usage anywhere → ok=false.
	if _, ok := p.ParseUsage([]byte(`{"id":"resp_1"}`)); ok {
		t.Fatal("payload without usage must return ok=false")
	}
}

func TestLookupFormatCodexStyle(t *testing.T) {
	for _, f := range []string{"codex", "openai-response", "unknown"} {
		p := LookupFormat(f)
		if p.AppendDone {
			t.Fatalf("%s must not append [DONE]", f)
		}
		// data:-framed passthrough
		if got := p.Frame([]byte("data: {}")); !bytes.Equal(got, []byte("data: {}")) {
			t.Fatalf("%s frame should passthrough, got %q", f, got)
		}
		// Extract strips the data: prefix for terminal/usage detection
		if got := p.Extract([]byte("data: {\"type\":\"response.completed\"}")); !bytes.HasPrefix(got, []byte("{")) {
			t.Fatalf("%s Extract should strip data:, got %q", f, got)
		}
		if !p.IsTerminal([]byte(`{"type":"response.completed"}`)) {
			t.Fatalf("%s terminal is response.completed", f)
		}
	}
}

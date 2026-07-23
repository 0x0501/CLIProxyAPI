package gateway

import (
	"bytes"
	"errors"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func feed(chunks ...cliproxyexecutor.StreamChunk) <-chan cliproxyexecutor.StreamChunk {
	ch := make(chan cliproxyexecutor.StreamChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch
}

func TestPipeStreamCodexUnchanged(t *testing.T) {
	var buf bytes.Buffer
	completed := `data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta","delta":"hi"}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(completed)},
	), LookupFormat("codex"), "gpt-5-codex")
	if !bytes.Contains(buf.Bytes(), []byte(`"delta":"hi"`)) || !bytes.Contains(buf.Bytes(), []byte("\n\nevent: tokenswim.usage")) {
		t.Fatalf("codex framing regressed: %s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("[DONE]")) {
		t.Fatal("codex must NOT emit [DONE]")
	}
}

func TestPipeStreamOpenAIFramesAndDone(t *testing.T) {
	var buf bytes.Buffer
	// openai chunks are RAW JSON (no data: prefix); the final one carries usage.
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`{"object":"chat.completion.chunk","choices":[{"delta":{"content":"hi"}}]}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`{"object":"chat.completion.chunk","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)},
	), LookupFormat("openai"), "gpt-5")
	out := buf.Bytes()
	if !bytes.Contains(out, []byte("data: {\"object\":\"chat.completion.chunk\"")) {
		t.Fatalf("openai chunks must be data:-framed: %s", out)
	}
	if !bytes.Contains(out, []byte("data: [DONE]\n\n")) {
		t.Fatalf("openai must terminate with data: [DONE]: %s", out)
	}
	if !bytes.Contains(out, []byte("\n\nevent: tokenswim.usage")) || !bytes.Contains(out, []byte(`"total_tokens":5`)) {
		t.Fatalf("openai usage frame missing: %s", out)
	}
}

// TestPipeStreamClaudeForwardsVerbatimAndAppendsUsage is the streaming
// billing-correctness gate: over a captured REAL codex->claude transcript (built
// by codexClaudeChunks via the live translator), PipeStream must forward the
// Anthropic events untouched — including the terminal chunk that bundles
// content_block_stop + message_delta + message_stop — and append exactly one
// tokenswim.usage frame carrying the tokens consolidated onto message_delta.
func TestPipeStreamClaudeForwardsVerbatimAndAppendsUsage(t *testing.T) {
	var buf bytes.Buffer
	claudeChunks := codexClaudeChunks(t)
	scs := make([]cliproxyexecutor.StreamChunk, 0, len(claudeChunks))
	for _, c := range claudeChunks {
		scs = append(scs, cliproxyexecutor.StreamChunk{Payload: c})
	}
	PipeStream(&buf, func() {}, feed(scs...), LookupFormat("claude"), "gpt-5")
	out := buf.Bytes()

	// Anthropic events forwarded verbatim (identity Frame).
	for _, want := range []string{
		"event: message_start",
		`"text":"Hello"`,
		"event: message_delta",
		"event: message_stop",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("claude event not forwarded verbatim: missing %q\n%s", want, out)
		}
	}
	// Exactly one appended usage control frame, carrying the consolidated tokens.
	if n := bytes.Count(out, []byte("event: tokenswim.usage")); n != 1 {
		t.Fatalf("want exactly one tokenswim.usage frame, got %d\n%s", n, out)
	}
	if !bytes.Contains(out, []byte(`"input_tokens":8`)) ||
		!bytes.Contains(out, []byte(`"output_tokens":7`)) {
		t.Fatalf("usage frame missing consolidated tokens: %s", out)
	}
	if bytes.Contains(out, []byte("[DONE]")) {
		t.Fatal("claude must NOT emit [DONE]")
	}
}

func TestPipeStreamOnChunkErrorAppendsErrorFrame(t *testing.T) {
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta"}`)},
		cliproxyexecutor.StreamChunk{Err: errors.New("connection reset")},
	), LookupFormat("codex"), "m")
	if !bytes.Contains(buf.Bytes(), []byte("event: tokenswim.error")) || !bytes.Contains(buf.Bytes(), []byte(`"disposition":"upstream_error"`)) {
		t.Fatalf("error frame not appended: %s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("\n\nevent: tokenswim.error")) {
		t.Fatalf("preceding forwarded frame not closed with blank line before error frame: %s", buf.String())
	}
}

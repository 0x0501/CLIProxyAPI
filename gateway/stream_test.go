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

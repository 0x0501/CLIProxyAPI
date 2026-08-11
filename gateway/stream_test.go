package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
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

// openAIStyleSSEEvents splits an SSE body the way OpenAI's SSEDecoder does:
// blank-line delimited events, each event's data: lines joined with "\n".
// Returns the joined data payload per event (skipping tokenswim.* control
// frames and [DONE]). A well-framed stream yields one JSON object per event.
func openAIStyleSSEEvents(body []byte) []string {
	var out []string
	for _, event := range bytes.Split(body, []byte("\n\n")) {
		if len(bytes.TrimSpace(event)) == 0 {
			continue
		}
		trimmed := bytes.TrimLeft(event, "\r\n")
		if bytes.HasPrefix(trimmed, []byte("event: tokenswim.")) {
			continue
		}
		var dataParts []string
		var eventName string
		for _, line := range bytes.Split(event, []byte("\n")) {
			l := bytes.TrimRight(line, "\r")
			if bytes.HasPrefix(l, []byte("event:")) {
				eventName = string(bytes.TrimSpace(l[len("event:"):]))
				continue
			}
			if bytes.HasPrefix(l, []byte("data:")) {
				part := string(bytes.TrimPrefix(l, []byte("data:")))
				// OpenAI trims one leading space after "data:".
				if len(part) > 0 && part[0] == ' ' {
					part = part[1:]
				}
				dataParts = append(dataParts, part)
			}
		}
		payload := strings.Join(dataParts, "\n")
		if payload == "[DONE]" {
			continue
		}
		// An event: field with no data: is what triggers
		// JSON.parse("") → "Unexpected end of JSON input" in the OpenAI SDK.
		if payload == "" {
			if eventName != "" {
				out = append(out, "") // signal empty-data event to the assert helper
			}
			continue
		}
		out = append(out, payload)
	}
	return out
}

// assertEachEventIsJSON is the client-compat gate: every non-control SSE event
// must JSON.parse on its own. Glued multi-chunk events and event:-only frames
// fail here exactly like the OpenAI SDK does in production.
func assertEachEventIsJSON(t *testing.T, body []byte) {
	t.Helper()
	events := openAIStyleSSEEvents(body)
	if len(events) == 0 {
		t.Fatalf("no client-visible SSE events in body:\n%s", body)
	}
	for i, payload := range events {
		if payload == "" {
			t.Fatalf("event %d has empty data (OpenAI SDK: Unexpected end of JSON input):\n%s", i, body)
		}
		if !json.Valid([]byte(payload)) {
			t.Fatalf("event %d is not a single JSON value (OpenAI SDK would throw):\n%s\n--- body ---\n%s", i, payload, body)
		}
	}
}

func TestPipeStreamCodexUnchanged(t *testing.T) {
	var buf bytes.Buffer
	completed := `data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`
	// No blank-line chunks between data: lines — the assembler must still emit
	// one SSE event per data: line (missing delimiters are the intermittent
	// responses failure mode).
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta","delta":"hi"}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(completed)},
	), LookupFormat("codex"), "gpt-5-codex", nil)
	if !bytes.Contains(buf.Bytes(), []byte(`"delta":"hi"`)) || !bytes.Contains(buf.Bytes(), []byte("\n\nevent: tokenswim.usage")) {
		t.Fatalf("codex framing regressed: %s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("[DONE]")) {
		t.Fatal("codex must NOT emit [DONE]")
	}
	assertEachEventIsJSON(t, buf.Bytes())
	if n := len(openAIStyleSSEEvents(buf.Bytes())); n != 2 {
		t.Fatalf("want 2 client events, got %d:\n%s", n, buf.String())
	}
}

// TestPipeStreamResponsesLineOriented is the responses-protocol gate: the
// codex executor feeds Scanner lines (event: / data: / blank) as separate
// chunks. Closing every non-empty chunk with "\n\n" would emit an event:-only
// frame whose data is "" — OpenAI SDK throws "Unexpected end of JSON input".
func TestPipeStreamResponsesLineOriented(t *testing.T) {
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`event: response.created`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.created","response":{"id":"r1"}}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(``)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`event: response.output_text.delta`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta","delta":"hi"}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(``)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`event: response.completed`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"id":"r1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(``)},
	), LookupFormat("openai-response"), "gpt-5", nil)
	out := buf.Bytes()
	assertEachEventIsJSON(t, out)
	// Three logical events: created, delta, completed.
	if n := len(openAIStyleSSEEvents(out)); n != 3 {
		t.Fatalf("want 3 client events, got %d:\n%s", n, out)
	}
	// Bare event: lines must NOT form their own blank-line-terminated events.
	if bytes.Contains(out, []byte("event: response.created\n\n")) {
		t.Fatalf("event: line was closed before its data: line:\n%s", out)
	}
	if !bytes.Contains(out, []byte("event: response.created\ndata: ")) {
		t.Fatalf("event: + data: must share one SSE event:\n%s", out)
	}
}

func TestPipeStreamOpenAIFramesAndDone(t *testing.T) {
	var buf bytes.Buffer
	// openai chunks are RAW JSON (no data: prefix); the final one carries usage.
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`{"object":"chat.completion.chunk","choices":[{"delta":{"content":"hi"}}]}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`{"object":"chat.completion.chunk","choices":[{"delta":{"content":"!"}}]}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(`{"object":"chat.completion.chunk","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)},
	), LookupFormat("openai"), "gpt-5", nil)
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
	// Regression: each non-terminal chunk must be its own blank-line-terminated
	// SSE event so the OpenAI SDK never joins multiple JSON objects into one parse.
	assertEachEventIsJSON(t, out)
	if n := len(openAIStyleSSEEvents(out)); n != 3 {
		t.Fatalf("want 3 client events (2 deltas + terminal), got %d:\n%s", n, out)
	}
}

// TestPipeStreamOpenAIAlreadyNewlinedPayload still emits one event per chunk
// when a payload already carries a trailing newline (some executors do).
func TestPipeStreamOpenAIAlreadyNewlinedPayload(t *testing.T) {
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte("{\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n")},
		cliproxyexecutor.StreamChunk{Payload: []byte("{\"object\":\"chat.completion.chunk\",\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")},
	), LookupFormat("openai"), "gpt-5", nil)
	assertEachEventIsJSON(t, buf.Bytes())
	if n := len(openAIStyleSSEEvents(buf.Bytes())); n != 2 {
		t.Fatalf("want 2 client events, got %d:\n%s", n, buf.String())
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
	PipeStream(&buf, func() {}, feed(scs...), LookupFormat("claude"), "gpt-5", nil)
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
	// Each data: payload across the stream must still be independently parseable
	// under OpenAI-style blank-line splitting (Anthropic clients are similar).
	assertEachEventIsJSON(t, out)
}

// TestPipeStreamClaudeChunkMissingTrailingBlank still isolates events when a
// translator chunk forgets the final "\n\n".
func TestPipeStreamClaudeChunkMissingTrailingBlank(t *testing.T) {
	var buf bytes.Buffer
	// One complete event without trailing blank line, then another.
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\"}}")},
		cliproxyexecutor.StreamChunk{Payload: []byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}")},
	), LookupFormat("claude"), "gpt-5", nil)
	assertEachEventIsJSON(t, buf.Bytes())
	if n := len(openAIStyleSSEEvents(buf.Bytes())); n != 2 {
		t.Fatalf("want 2 client events, got %d:\n%s", n, buf.String())
	}
}

func TestPipeStreamOnChunkErrorAppendsErrorFrame(t *testing.T) {
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta"}`)},
		cliproxyexecutor.StreamChunk{Err: errors.New("connection reset")},
	), LookupFormat("codex"), "m", nil)
	if !bytes.Contains(buf.Bytes(), []byte("event: tokenswim.error")) || !bytes.Contains(buf.Bytes(), []byte(`"disposition":"upstream_error"`)) {
		t.Fatalf("error frame not appended: %s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("\n\nevent: tokenswim.error")) {
		t.Fatalf("preceding forwarded frame not closed with blank line before error frame: %s", buf.String())
	}
	assertEachEventIsJSON(t, buf.Bytes())
}

// TestPipeStreamBareEventThenError drops a dangling event: line so the client
// never JSON.parse("") before tokenswim.error (review #6).
func TestPipeStreamBareEventThenError(t *testing.T) {
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`event: response.created`)},
		cliproxyexecutor.StreamChunk{Err: errors.New("boom")},
	), LookupFormat("openai-response"), "m", nil)
	out := buf.Bytes()
	if bytes.Contains(out, []byte("event: response.created")) {
		t.Fatalf("dangling event: must be dropped, not emitted with empty data:\n%s", out)
	}
	if !bytes.Contains(out, []byte("event: tokenswim.error")) {
		t.Fatalf("error frame missing:\n%s", out)
	}
	// Only the control frame is client-visible; assertEachEventIsJSON skips
	// tokenswim.* so ensure no empty-data non-control events exist via the helper
	// path used for mixed streams (empty openAIStyle list is fine here).
	if events := openAIStyleSSEEvents(out); len(events) != 0 {
		t.Fatalf("want no client data events before error, got %v\n%s", events, out)
	}
}

// TestPipeStreamEventLineWithTrailingNewline trims before multi-line detection
// so "event: foo\n" is not closed as an empty-data event.
func TestPipeStreamEventLineWithTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte("event: response.created\n")},
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.created","response":{"id":"r1"}}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte(``)},
	), LookupFormat("openai-response"), "m", nil)
	out := buf.Bytes()
	if bytes.Contains(out, []byte("event: response.created\n\n")) {
		t.Fatalf("event: line closed before data:\n%s", out)
	}
	if !bytes.Contains(out, []byte("event: response.created\ndata: ")) {
		t.Fatalf("event: + data: must share one SSE event:\n%s", out)
	}
	assertEachEventIsJSON(t, out)
	if n := len(openAIStyleSSEEvents(out)); n != 1 {
		t.Fatalf("want 1 client event, got %d:\n%s", n, out)
	}
}

// TestPipeStreamBareEventAtEOF drops dangling event: with no data before the
// stream ends (no usage frame either when nothing was terminal).
func TestPipeStreamBareEventAtEOF(t *testing.T) {
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`event: response.created`)},
	), LookupFormat("openai-response"), "m", nil)
	out := buf.Bytes()
	if bytes.Contains(out, []byte("event: response.created")) {
		t.Fatalf("dangling event: at EOF must be dropped:\n%s", out)
	}
	if events := openAIStyleSSEEvents(out); len(events) != 0 {
		t.Fatalf("want no client data events, got %v\n%s", events, out)
	}
}

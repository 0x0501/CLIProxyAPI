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

func TestPipeStreamForwardsAndAppendsUsage(t *testing.T) {
	p, _ := Lookup("codex")
	var buf bytes.Buffer
	completed := `data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta","delta":"hi"}`)},
		cliproxyexecutor.StreamChunk{Payload: []byte("")}, // blank separator preserved
		cliproxyexecutor.StreamChunk{Payload: []byte(completed)},
	), p, "gpt-5-codex")

	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte(`"delta":"hi"`)) {
		t.Fatal("delta frame not forwarded")
	}
	if !bytes.Contains(buf.Bytes(), []byte("event: tokenswim.usage")) || !bytes.Contains(buf.Bytes(), []byte(`"total_tokens":5`)) {
		t.Fatalf("usage frame not appended: %s", out)
	}
}

func TestPipeStreamOnChunkErrorAppendsErrorFrame(t *testing.T) {
	p, _ := Lookup("codex")
	var buf bytes.Buffer
	PipeStream(&buf, func() {}, feed(
		cliproxyexecutor.StreamChunk{Payload: []byte(`data: {"type":"response.output_text.delta"}`)},
		cliproxyexecutor.StreamChunk{Err: errors.New("connection reset")},
	), p, "m")
	if !bytes.Contains(buf.Bytes(), []byte("event: tokenswim.error")) || !bytes.Contains(buf.Bytes(), []byte(`"disposition":"upstream_error"`)) {
		t.Fatalf("error frame not appended: %s", buf.String())
	}
}

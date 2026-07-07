package gateway

import (
	"bytes"
	"testing"
)

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

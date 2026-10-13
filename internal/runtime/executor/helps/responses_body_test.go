package helps

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

type timedResponseReader struct {
	read func([]byte) (int, error)
}

func (r timedResponseReader) Read(p []byte) (int, error) { return r.read(p) }

func TestReadResponsesBodyObservesFirstTokenBeforeCompletion(t *testing.T) {
	r := NewUsageReporter(context.Background(), "codex", "gpt-test", nil)
	r.StartResponseTTFT()
	parts := []string{
		"event: response.created\r\ndata: {\"type\":\"response.created\"}\r\n\r\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}\n\n",
		"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"thinking\"}\n\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"many tokens in one event\"}\n\n",
	}
	var firstToken time.Duration
	i := 0
	reader := timedResponseReader{read: func(p []byte) (int, error) {
		if i < 3 && r.hasTokenTTFT() {
			t.Fatal("metadata or empty deltas were counted as the first token")
		}
		if i == 3 {
			if !r.hasTokenTTFT() {
				t.Fatal("first token was not observed before reading later output")
			}
			firstToken = r.ttftDuration()
		}
		if i == len(parts) {
			return 0, io.EOF
		}
		n := copy(p, parts[i])
		i++
		return n, nil
	}}
	got, err := ReadResponsesBody(reader, r)
	want := ""
	for _, part := range parts {
		want += part
	}
	if err != nil || string(got) != want {
		t.Fatalf("body changed: err=%v, body=%q", err, got)
	}
	if r.ttftDuration() != firstToken || !r.buildRecord(usage.Detail{}, false, usage.Failure{}).TTFTIsToken {
		t.Fatal("later chunks changed the first-token measurement")
	}
}

func TestReadResponsesBodyPreservesPartialDataAndReadError(t *testing.T) {
	wantErr := errors.New("upstream reset")
	got, err := ReadResponsesBody(timedResponseReader{read: func(p []byte) (int, error) {
		return copy(p, "data: partial"), wantErr
	}}, nil)
	if string(got) != "data: partial" || !errors.Is(err, wantErr) {
		t.Fatalf("partial read: data=%q, err=%v", got, err)
	}
}

func TestReadResponsesBodyTerminalUsageDoesNotInventFirstToken(t *testing.T) {
	for _, event := range []string{"response.completed", "response.done", "response.incomplete", "response.failed", "error"} {
		t.Run(event, func(t *testing.T) {
			r := NewUsageReporter(context.Background(), "codex", "gpt-test", nil)
			r.StartResponseTTFT()
			body := "data: {\"type\":\"" + event + "\",\"response\":{\"usage\":{\"output_tokens\":120}}}\n\n"
			if _, err := ReadResponsesBody(strings.NewReader(body), r); err != nil {
				t.Fatal(err)
			}
			record := r.buildRecord(usage.Detail{OutputTokens: 120}, false)
			if record.TTFTIsToken || r.IsTTFTSet() {
				t.Fatal("terminal usage must not claim an observed output token")
			}
		})
	}
}

func TestTokenTTFTSharesLatencyOrigin(t *testing.T) {
	for _, prep := range []time.Duration{0, 10 * time.Second} {
		r := NewUsageReporter(context.Background(), "codex", "gpt-test", nil)
		r.requestedAt = time.Now().Add(-prep)
		r.StartResponseTTFT()
		ObserveResponsesTokenEvent(r, []byte(`{"type":"response.output_text.delta","delta":"first token"}`))
		record := r.buildRecord(usage.Detail{OutputTokens: 120}, false)
		if !record.TTFTIsToken || record.TTFT < prep {
			t.Fatalf("TTFT must start at executor creation: prep=%v, TTFT=%v", prep, record.TTFT)
		}
		generation := record.Latency - record.TTFT
		if generation < 0 || generation > time.Second {
			t.Fatalf("preparation leaked into generation interval: prep=%v, generation=%v", prep, generation)
		}
	}
}

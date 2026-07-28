package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// Fixture from the 2026-07-28 cache-write investigation: a codex terminal event
// carrying every bucket non-zero. Cache write (300) and reasoning (10) are the
// two the client-facing translations lose — chat renames the field, claude has
// no wire slot for either.
const (
	fixtureModel = "gpt-5.6-terra"

	rawCreatedEvent = `{"type":"response.created","response":{"id":"resp_repro","created_at":1753670000,"model":"gpt-5.6-terra","status":"in_progress"}}`

	rawCompletedEvent = `{"type":"response.completed","response":{"id":"resp_repro","created_at":1753670000,"model":"gpt-5.6-terra","status":"completed","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":400,"cache_write_tokens":300},"output_tokens":50,"output_tokens_details":{"reasoning_tokens":10},"total_tokens":1050}}}`
)

// translatedChunks drives the REAL codex->target translator over the fixture
// stream and returns the chunks exactly as the executor emits them. The
// translator input must carry the "data: " SSE prefix, and a response.created
// frame must be fed first to initialize stream state.
func translatedChunks(t *testing.T, to sdktranslator.Format) []cliproxyexecutor.StreamChunk {
	t.Helper()
	ctx := context.Background()
	var param any
	var out []cliproxyexecutor.StreamChunk
	for _, event := range []string{rawCreatedEvent, rawCompletedEvent} {
		frames := sdktranslator.TranslateStream(ctx, sdktranslator.FormatCodex, to,
			fixtureModel, []byte(`{}`), []byte(`{}`), []byte("data: "+event), &param)
		for _, f := range frames {
			out = append(out, cliproxyexecutor.StreamChunk{Payload: f})
		}
	}
	return out
}

// usageFrame decodes the trailing tokenswim.usage control frame — the only
// thing the worker settles on, and therefore the only thing these tests assert.
func usageFrame(t *testing.T, out []byte) UsagePayload {
	t.Helper()
	const head = "event: tokenswim.usage\ndata: "
	i := bytes.Index(out, []byte(head))
	if i < 0 {
		t.Fatalf("no tokenswim.usage frame in output:\n%s", out)
	}
	body := out[i+len(head):]
	if end := bytes.IndexByte(body, '\n'); end >= 0 {
		body = body[:end]
	}
	var u UsagePayload
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("tokenswim.usage frame is not valid JSON (%v): %s", err, body)
	}
	return u
}

func rawFixtureDetail(t *testing.T) usage.Detail {
	t.Helper()
	d, ok := helps.ParseCodexUsage([]byte(rawCompletedEvent))
	if !ok {
		t.Fatal("fixture: raw codex terminal event must yield usage")
	}
	return d
}

func assertFixtureBuckets(t *testing.T, u UsagePayload) {
	t.Helper()
	if u.InputTokens != 1000 || u.OutputTokens != 50 || u.TotalTokens != 1050 {
		t.Fatalf("input/output/total = %d/%d/%d, want 1000/50/1050", u.InputTokens, u.OutputTokens, u.TotalTokens)
	}
	if u.CacheReadTokens != 400 {
		t.Fatalf("cache read = %d, want 400", u.CacheReadTokens)
	}
	if u.CacheCreationTokens != 300 {
		t.Fatalf("cache write = %d, want 300", u.CacheCreationTokens)
	}
	if u.ReasoningTokens != 10 {
		t.Fatalf("reasoning = %d, want 10", u.ReasoningTokens)
	}
	if !u.BreakdownComplete {
		t.Fatalf("breakdown must be complete: %+v", u.TokenBreakdown)
	}
	if u.TokenBreakdown.Input.UncachedTokens != 300 ||
		u.TokenBreakdown.Input.CacheReadTokens != 400 ||
		u.TokenBreakdown.Input.CacheWriteTokens != 300 {
		t.Fatalf("input breakdown = %+v, want uncached 300 / read 400 / write 300", u.TokenBreakdown.Input)
	}
}

// The settlement frame must carry what UPSTREAM reported, identically on all
// three client protocols — including the two buckets the chat and claude
// translations cannot represent (ADR 0019).
func TestPipeStreamUsageFrameFollowsRawUpstreamOnEveryClientFormat(t *testing.T) {
	raw := rawFixtureDetail(t)
	for _, tc := range []struct {
		name    string
		profile string
		to      sdktranslator.Format
	}{
		{"responses", "openai-response", sdktranslator.FormatOpenAIResponse},
		{"chat", "openai", sdktranslator.FormatOpenAI},
		{"messages", "claude", sdktranslator.FormatClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, capture := NewUsageCapture(context.Background(), fixtureModel)
			usage.PublishRecord(ctx, usage.Record{Model: fixtureModel, Detail: raw})

			var buf bytes.Buffer
			PipeStream(&buf, func() {}, feed(translatedChunks(t, tc.to)...),
				LookupFormat(tc.profile), fixtureModel, capture)

			assertFixtureBuckets(t, usageFrame(t, buf.Bytes()))
		})
	}
}

// Two requests in flight at once, each with its own raw detail published before
// either stream resolves: neither may settle on the other's tokens.
func TestUsageCaptureKeepsConcurrentRequestsIsolated(t *testing.T) {
	const otherModel = "gpt-other"
	detailA := rawFixtureDetail(t)
	detailB := usage.EnsureTokenBreakdownForProvider(usage.Detail{
		InputTokens:         20,
		CachedTokens:        5,
		CacheReadTokens:     5,
		CacheCreationTokens: 7,
		OutputTokens:        3,
		ReasoningTokens:     1,
	}, "codex", "")

	ctxA, capA := NewUsageCapture(context.Background(), fixtureModel)
	ctxB, capB := NewUsageCapture(context.Background(), otherModel)

	// Publish out of order, both before either stream is piped.
	usage.PublishRecord(ctxB, usage.Record{Model: otherModel, Detail: detailB})
	usage.PublishRecord(ctxA, usage.Record{Model: fixtureModel, Detail: detailA})

	// The claude profile loses cache write and reasoning in translation, so a
	// frame carrying either can only have come from that request's own capture.
	chunks := translatedChunks(t, sdktranslator.FormatClaude)
	var bufA, bufB bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		PipeStream(&bufA, func() {}, feed(chunks...), LookupFormat("claude"), fixtureModel, capA)
	}()
	go func() {
		defer wg.Done()
		PipeStream(&bufB, func() {}, feed(chunks...), LookupFormat("claude"), otherModel, capB)
	}()
	wg.Wait()

	if u := usageFrame(t, bufA.Bytes()); u.CacheCreationTokens != 300 || u.ReasoningTokens != 10 {
		t.Fatalf("request A settled on foreign tokens: cache write %d, reasoning %d", u.CacheCreationTokens, u.ReasoningTokens)
	}
	if u := usageFrame(t, bufB.Bytes()); u.CacheCreationTokens != 7 || u.ReasoningTokens != 1 {
		t.Fatalf("request B settled on foreign tokens: cache write %d, reasoning %d", u.CacheCreationTokens, u.ReasoningTokens)
	}
}

// Story 7: a translated stream that disagrees with raw upstream leaves a log
// line instead of silently corrupting billing. Raw wins the frame. The line is
// Debug because the claude path diverges structurally on every request.
func TestPipeStreamLogsDivergenceAndPrefersRaw(t *testing.T) {
	raw := rawFixtureDetail(t)

	prevLevel := logrus.GetLevel()
	logrus.SetLevel(logrus.DebugLevel)
	defer logrus.SetLevel(prevLevel)

	for _, tc := range []struct {
		name          string
		profile       string
		to            sdktranslator.Format
		wantDivergent bool
	}{
		// claude drops cache write and reasoning: materially divergent.
		{"messages", "claude", sdktranslator.FormatClaude, true},
		// responses is a lossless passthrough: nothing to report.
		{"responses", "openai-response", sdktranslator.FormatOpenAIResponse, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := test.NewGlobal()
			defer hook.Reset()

			ctx, capture := NewUsageCapture(context.Background(), fixtureModel)
			usage.PublishRecord(ctx, usage.Record{Model: fixtureModel, Detail: raw})

			var buf bytes.Buffer
			PipeStream(&buf, func() {}, feed(translatedChunks(t, tc.to)...),
				LookupFormat(tc.profile), fixtureModel, capture)

			assertFixtureBuckets(t, usageFrame(t, buf.Bytes()))

			var logged int
			for _, e := range hook.AllEntries() {
				if e.Level == logrus.DebugLevel && strings.Contains(e.Message, "disagrees with raw upstream usage") {
					logged++
				}
			}
			if tc.wantDivergent && logged == 0 {
				t.Fatal("translated stream lost buckets but no divergence was logged")
			}
			if !tc.wantDivergent && logged != 0 {
				t.Fatalf("agreeing sources logged %d divergences", logged)
			}
		})
	}
}

// When no raw record arrives the gateway must still emit a usage frame, built
// by re-parsing the translated stream exactly as it did before ADR 0019. The
// per-format expectations below are that fallback's known fidelity: responses
// is lossless, chat recovers cache write only through the parser's alias entry,
// and claude structurally cannot carry cache write or reasoning at all — which
// is why the raw-capture test above is not merely re-asserting re-parse.
func TestPipeStreamFallsBackToTranslatedReparseWithoutRawRecord(t *testing.T) {
	for _, tc := range []struct {
		name           string
		profile        string
		to             sdktranslator.Format
		wantCacheWrite int64
		wantReasoning  int64
	}{
		{"responses", "openai-response", sdktranslator.FormatOpenAIResponse, 300, 10},
		{"chat", "openai", sdktranslator.FormatOpenAI, 300, 10},
		{"messages", "claude", sdktranslator.FormatClaude, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A real slot that never receives a record: this exercises the
			// bounded wait and its timeout, not the nil short-circuit.
			_, capture := NewUsageCapture(context.Background(), fixtureModel)

			start := time.Now()
			var buf bytes.Buffer
			PipeStream(&buf, func() {}, feed(translatedChunks(t, tc.to)...),
				LookupFormat(tc.profile), fixtureModel, capture)
			if elapsed := time.Since(start); elapsed > 4*usageCaptureWait {
				t.Fatalf("fallback took %s, want a bounded wait of ~%s", elapsed, usageCaptureWait)
			}

			u := usageFrame(t, buf.Bytes())
			if u.CacheCreationTokens != tc.wantCacheWrite {
				t.Fatalf("cache write = %d, want %d", u.CacheCreationTokens, tc.wantCacheWrite)
			}
			if u.ReasoningTokens != tc.wantReasoning {
				t.Fatalf("reasoning = %d, want %d", u.ReasoningTokens, tc.wantReasoning)
			}
			if u.TokenBreakdown.Input.CacheWriteTokens != tc.wantCacheWrite {
				t.Fatalf("input breakdown = %+v, want cache write %d", u.TokenBreakdown.Input, tc.wantCacheWrite)
			}
		})
	}
}

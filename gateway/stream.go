package gateway

import (
	"bytes"
	"io"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// writeCompleteSSEEvent writes one self-contained SSE event terminated by a
// blank line. OpenAI's SSEDecoder (and every standards-compliant client) splits
// on "\n\n" and JSON.parse's the joined data: lines — so each event must carry
// exactly one JSON object.
func writeCompleteSSEEvent(w io.Writer, framed []byte) {
	framed = bytes.TrimRight(framed, "\r\n")
	if len(framed) == 0 {
		return
	}
	_, _ = w.Write(framed)
	_, _ = w.Write([]byte("\n\n"))
}

// sseAssembler rebuilds OpenAI-compatible SSE events from line-oriented chunks.
//
// The codex executor scans upstream with bufio.Scanner (one line per chunk). The
// openai-response translator passes those lines through, so a single logical
// event arrives as separate chunks:
//
//	"event: response.created"
//	"data: {...}"
//	""                  ← blank delimiter (sometimes missing under load / translators)
//
// Two failure modes this closes:
//
//  1. Emitting "\n\n" after every non-empty chunk closes the event after a bare
//     "event:" line → OpenAI SDK JSON.parse("") → "Unexpected end of JSON input".
//  2. Emitting only "\n" after every chunk, when blank lines are missing, glues
//     multiple data: JSON objects into one event → "Unexpected non-whitespace
//     character after JSON" / "Unexpected end of JSON input".
//
// Assembly rules match how OpenAI frames its own Responses SSE:
// each data: line is its own event; a preceding event: field binds to the next
// data: line; a blank line flushes only when data is present (a dangling event:
// name is dropped, never emitted with empty data).
type sseAssembler struct {
	buf     bytes.Buffer
	hasData bool
}

func (a *sseAssembler) writeField(line []byte) {
	if a.buf.Len() > 0 {
		a.buf.WriteByte('\n')
	}
	a.buf.Write(line)
}

// flush emits a pending event only when it has a data: line. A bare event: (or
// id:/comment-only) buffer is discarded — never written as an empty-data SSE
// event that would make clients JSON.parse("").
func (a *sseAssembler) flush(w io.Writer) {
	if !a.hasData {
		a.buf.Reset()
		return
	}
	writeCompleteSSEEvent(w, a.buf.Bytes())
	a.buf.Reset()
	a.hasData = false
}

func (a *sseAssembler) push(w io.Writer, framed []byte) {
	// Trim before classifying. A Scanner line that still carries its trailing
	// "\n" must not be treated as a pre-framed multi-line event (that path
	// would emit "event: foo\n\n" with empty data).
	line := bytes.TrimRight(framed, "\r\n")
	if len(line) == 0 {
		// Blank line = SSE event delimiter.
		a.flush(w)
		return
	}

	// Pre-framed multi-line payload (claude translator emits complete events,
	// sometimes several, already "\n\n"-terminated). Don't mix with pending lines.
	if bytes.Contains(line, []byte("\n")) {
		a.flush(w)
		writeCompleteSSEEvent(w, line)
		return
	}

	switch {
	case bytes.HasPrefix(line, []byte("event:")):
		// A new event: field starts a new SSE event. Flush only when the pending
		// event already has data — otherwise replace a dangling event: name.
		if a.hasData {
			a.flush(w)
		} else {
			a.buf.Reset()
		}
		a.writeField(line)
	case bytes.HasPrefix(line, []byte("data:")):
		// OpenAI puts exactly one JSON object per event. A second data: line is a
		// new event even when the blank delimiter was dropped.
		if a.hasData {
			a.flush(w)
		}
		a.writeField(line)
		a.hasData = true
	default:
		// id: / retry: / :comment — attach to the current event.
		a.writeField(line)
	}
}

// PipeStream forwards upstream SSE chunks framed per the given FormatProfile
// (verbatim for already-framed formats like codex, "data: "-prefixed for raw-JSON
// formats like openai), and after the upstream stream ends appends exactly one
// control frame: tokenswim.usage on success, tokenswim.error on a mid-stream error.
//
// The usage frame is built from the raw upstream detail the executor captured,
// falling back to re-parsing the translated terminal frame (ADR 0019).
//
// Framing (the "Unexpected end of JSON input" class of client failures):
//
//   - AtomicChunks (openai): each StreamChunk is one complete chat.completion.chunk
//     JSON object after Frame adds "data: ". It MUST be its own blank-line-terminated
//     SSE event — a single trailing "\n" glued every non-terminal chunk into one
//     mega-event and JSON.parse failed in the OpenAI SDK.
//   - otherwise (openai-response / codex / claude): line-oriented or pre-framed
//     multi-line events, reassembled by sseAssembler so missing blank lines and
//     event:/data: splits cannot produce empty-data or multi-JSON events.
//   - AppendDone is orthogonal: only whether to emit a trailing data: [DONE].
func PipeStream(w io.Writer, flush func(), chunks <-chan cliproxyexecutor.StreamChunk, profile FormatProfile, model string, capture *UsageCapture) {
	var reparsed usage.Detail
	var haveReparsed bool
	var asm sseAssembler
	for chunk := range chunks {
		if chunk.Err != nil {
			// chunk.Err may carry a StatusError (e.g. mid-stream usage-limit -> 429);
			// ClassifyExecError extracts it, else falls back to upstream_error.
			// Flush a pending data-bearing event; drop a bare event: so the error
			// frame is never preceded by empty-data JSON for the client to parse.
			asm.flush(w)
			_, _ = w.Write(FormatErrorEvent(ClassifyExecError(chunk.Err)))
			flush()
			return
		}
		raw := profile.Extract(chunk.Payload)
		framed := profile.Frame(chunk.Payload)

		if profile.AtomicChunks {
			// openai chat.completions: whole-event chunks (raw JSON → "data: …").
			writeCompleteSSEEvent(w, framed)
		} else {
			// openai-response / codex / claude.
			asm.push(w, framed)
		}

		// The terminal frame carries usage; after it is framed, append Done/usage.
		if profile.IsTerminal(raw) {
			// Line-oriented terminal data: is sitting in the assembler — close it
			// before tokenswim.usage so the control frame is its own SSE event.
			if !profile.AtomicChunks {
				asm.flush(w)
			}
			if profile.AppendDone {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
			}
			if d, ok := profile.ParseUsage(raw); ok {
				reparsed, haveReparsed = d, true
			}
			flush()
			continue
		}
		flush()
	}
	// Incomplete stream: emit a pending data-bearing event; drop bare event:.
	asm.flush(w)
	// The bounded wait for raw upstream detail sits here, after the last
	// translated chunk, so it can never delay a client-visible token.
	if d, ok := ResolveUsage(capture, reparsed, haveReparsed, model); ok {
		_, _ = w.Write(FormatUsageEvent(UsageFromDetail(d, model)))
		flush()
	}
}

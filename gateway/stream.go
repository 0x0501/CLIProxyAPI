package gateway

import (
	"io"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// PipeStream forwards upstream SSE chunks framed per the given FormatProfile
// (verbatim for already-framed formats like codex, "data: "-prefixed for raw-JSON
// formats like openai), and after the upstream stream ends appends exactly one
// control frame: tokenswim.usage on success, tokenswim.error on a mid-stream error.
func PipeStream(w io.Writer, flush func(), chunks <-chan cliproxyexecutor.StreamChunk, profile FormatProfile, model string) {
	for chunk := range chunks {
		if chunk.Err != nil {
			// chunk.Err may carry a StatusError (e.g. mid-stream usage-limit -> 429);
			// ClassifyExecError extracts it, else falls back to upstream_error.
			// Leading "\n" closes the preceding forwarded frame's single "\n" into
			// the "\n\n" SSE boundary the worker's splitControlFrames requires.
			_, _ = w.Write([]byte("\n"))
			_, _ = w.Write(FormatErrorEvent(ClassifyExecError(chunk.Err)))
			flush()
			return
		}
		raw := profile.Extract(chunk.Payload)
		framed := profile.Frame(chunk.Payload)
		// The terminal frame carries usage; forward it, then append our summary.
		if profile.IsTerminal(raw) {
			_, _ = w.Write(framed)
			// "\n\n" (not "\n") closes this SSE event before the tokenswim.usage
			// control frame, matching the worker's "\n\n"-delimited event split.
			_, _ = w.Write([]byte("\n\n"))
			if profile.AppendDone {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
			}
			if d, ok := profile.ParseUsage(raw); ok {
				_, _ = w.Write(FormatUsageEvent(UsageFromDetail(d, model)))
			}
			flush()
			continue
		}
		_, _ = w.Write(framed)
		_, _ = w.Write([]byte("\n"))
		flush()
	}
}

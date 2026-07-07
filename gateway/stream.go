package gateway

import (
	"bytes"
	"io"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// PipeStream forwards upstream SSE chunks verbatim (re-appending the one newline
// the executor stripped), and after the upstream stream ends appends exactly one
// control frame: tokenswim.usage on success, tokenswim.error on a mid-stream error.
func PipeStream(w io.Writer, flush func(), chunks <-chan cliproxyexecutor.StreamChunk, p Provider, model string) {
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
		// The completed frame carries usage; forward it, then append our summary.
		if p.IsCompleted(sseData(chunk.Payload)) {
			_, _ = w.Write(chunk.Payload)
			// "\n\n" (not "\n") closes this SSE event before the tokenswim.usage
			// control frame, matching the worker's "\n\n"-delimited event split.
			_, _ = w.Write([]byte("\n\n"))
			if d, ok := p.ParseUsage(sseData(chunk.Payload)); ok {
				_, _ = w.Write(FormatUsageEvent(UsageFromDetail(d, model)))
			}
			flush()
			continue
		}
		_, _ = w.Write(chunk.Payload)
		_, _ = w.Write([]byte("\n"))
		flush()
	}
}

// sseData strips a leading "data: " so provider predicates/parsers see raw JSON.
func sseData(line []byte) []byte {
	return bytes.TrimPrefix(line, []byte("data: "))
}

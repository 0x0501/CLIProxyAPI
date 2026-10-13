package helps

import (
	"bufio"
	"bytes"
	"io"
)

// ReadResponsesBody preserves the full upstream body while observing token
// events as they arrive. Non-stream client requests still use upstream SSE;
// observing only after io.ReadAll would measure completion instead of TTFT.
func ReadResponsesBody(body io.Reader, reporter *UsageReporter) ([]byte, error) {
	reader := bufio.NewReader(body)
	var data bytes.Buffer
	for {
		line, err := reader.ReadBytes('\n')
		data.Write(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			ObserveResponsesTokenEvent(reporter, bytes.TrimSpace(line[len("data:"):]))
		}
		if err == io.EOF {
			return data.Bytes(), nil
		}
		if err != nil {
			return data.Bytes(), err
		}
	}
}

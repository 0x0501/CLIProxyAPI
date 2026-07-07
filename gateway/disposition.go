package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

// Disposition is the final classification of an error (or success).
type Disposition string

const (
	DispositionOK            Disposition = "ok"
	DispositionInvalid       Disposition = "invalid"
	DispositionRevoked       Disposition = "revoked"
	DispositionExpired       Disposition = "expired"
	DispositionRateLimited   Disposition = "rate_limited"
	DispositionUpstreamError Disposition = "upstream_error"
)

// ErrorPayload is the structured error report sent to the client.
type ErrorPayload struct {
	Disposition    Disposition
	UpstreamStatus int
	Code           string
	Message        string
}

// Classify maps an HTTP status and upstream body to a Disposition.
// 401 → invalid (malformed token).
// 403 → revoked (unless code is another error, then map accordingly).
// 429 → rate_limited (transient).
// 5xx → upstream_error (transient).
// 400 (default for unknown 4xx) → invalid unless body says otherwise.
func Classify(status int, body string) Disposition {
	switch status {
	case 401:
		return DispositionInvalid
	case 403:
		// Extract code from body to see if it's account_deactivated (revoked) or something else.
		code := gjson.Get(body, "error.code").String()
		if code == "account_deactivated" {
			return DispositionRevoked
		}
		return DispositionRevoked // 403 defaults to revoked
	case 429:
		return DispositionRateLimited
	case 500, 502, 503, 504:
		return DispositionUpstreamError
	default:
		// Treat other 4xx as invalid (malformed request or token).
		if status >= 400 && status < 500 {
			return DispositionInvalid
		}
		// Treat other 5xx as upstream_error.
		if status >= 500 {
			return DispositionUpstreamError
		}
		// Default: unknown error.
		return DispositionUpstreamError
	}
}

// StatusFromExecErr extracts the HTTP status and body from an executor error.
// Executor errors implement cliproxyexecutor.StatusError (status code + Error() body).
// If the error is not a StatusError, returns (0, "").
func StatusFromExecErr(err error) (int, string) {
	var se executor.StatusError
	if errors.As(err, &se) {
		return se.StatusCode(), se.Error()
	}
	return 0, ""
}

// ClassifyExecError classifies an error from ExecuteStream or chunk.Err.
// It extracts the status and body, parses the error code from the body,
// and returns the full ErrorPayload.
func ClassifyExecError(err error) ErrorPayload {
	status, body := StatusFromExecErr(err)
	disposition := Classify(status, body)

	code := gjson.Get(body, "error.code").String()
	message := gjson.Get(body, "error.message").String()

	return ErrorPayload{
		Disposition:    disposition,
		UpstreamStatus: status,
		Code:           code,
		Message:        message,
	}
}

// ClassifyRefreshError classifies a failed token refresh.
// The refresh path returns a string-only error (no typed status):
// a permanent refusal (invalid_grant / HTTP 400 / 401) means the grant is dead -> revoked;
// anything else is transient -> upstream_error.
func ClassifyRefreshError(err error) ErrorPayload {
	errStr := err.Error()

	// Scan for permanent failure markers.
	if strings.Contains(errStr, "invalid_grant") ||
		strings.Contains(errStr, "refresh_token_reused") ||
		strings.Contains(errStr, "status 400") ||
		strings.Contains(errStr, "status 401") {
		return ErrorPayload{
			Disposition:    DispositionRevoked,
			UpstreamStatus: 0, // No typed status from refresh.
			Code:           "refresh_failed",
			Message:        errStr,
		}
	}

	// Extract any 5xx status mentioned in the error for display.
	upstreamStatus := 0
	statusRe := regexp.MustCompile(`status (\d{3})`)
	if match := statusRe.FindStringSubmatch(errStr); len(match) > 1 {
		if s, err := strconv.Atoi(match[1]); err == nil {
			upstreamStatus = s
		}
	}

	return ErrorPayload{
		Disposition:    DispositionUpstreamError,
		UpstreamStatus: upstreamStatus,
		Code:           "refresh_failed",
		Message:        errStr,
	}
}

// FormatErrorEvent formats an ErrorPayload as an SSE frame.
func FormatErrorEvent(p ErrorPayload) []byte {
	// Assemble the JSON payload.
	payload := map[string]interface{}{
		"disposition":     p.Disposition,
		"upstream_status": p.UpstreamStatus,
		"code":            p.Code,
		"message":         p.Message,
	}
	data, _ := json.Marshal(payload)

	// SSE frame: event: <event>\ndata: <json>\n\n
	return []byte(fmt.Sprintf("event: tokenswim.error\ndata: %s\n\n", data))
}

// HTTPStatus returns the HTTP status to send before streaming.
// If the upstream returned a sane status (401/403/429/5xx), return it.
// Otherwise, default to 502 (bad gateway).
func (p ErrorPayload) HTTPStatus() int {
	switch p.UpstreamStatus {
	case 401, 403, 429:
		return p.UpstreamStatus
	case 500, 502, 503, 504:
		return p.UpstreamStatus
	default:
		// Unknown or no upstream status; default to 502.
		return 502
	}
}

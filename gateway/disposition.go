package gateway

import (
	"errors"
	"strings"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type Disposition string

const (
	DispositionOK            Disposition = "ok"
	DispositionInvalid       Disposition = "invalid"
	DispositionRevoked       Disposition = "revoked"
	DispositionExpired       Disposition = "expired"
	DispositionRateLimited   Disposition = "rate_limited"
	DispositionUpstreamError Disposition = "upstream_error"
	// DispositionSessionLost: a continuation found no live upstream socket (or
	// the upstream no longer holds previous_response_id). Not a credential
	// signal; the caller resends the full input.
	DispositionSessionLost Disposition = "session_lost"
)

type ErrorPayload struct {
	Disposition    Disposition `json:"disposition"`
	UpstreamStatus int         `json:"upstream_status"`
	Code           string      `json:"code"`
	Message        string      `json:"message"`
	TTFTMs         *float64    `json:"ttft_ms,omitempty"`
}

// Classify maps an upstream HTTP status to a disposition. Credential-fatal
// statuses (401/402/403) map to invalid/revoked; 429 and 5xx are transient and
// MUST NOT mutate auth status downstream. Codex remaps capacity/usage-limit
// bodies to 429 before we see them, so 429 is always treated as transient.
func Classify(status int, _ string) Disposition {
	switch {
	case status == 401 || status == 402:
		return DispositionInvalid
	case status == 403:
		return DispositionRevoked
	case status == 429:
		return DispositionRateLimited
	case status >= 500:
		return DispositionUpstreamError
	case status >= 200 && status < 300:
		return DispositionOK
	default:
		// Other 4xx (e.g. 400 bad request) are client/payload errors, not
		// credential problems — transient, status untouched.
		return DispositionUpstreamError
	}
}

// StatusFromExecErr pulls the upstream HTTP status + raw body out of an executor
// error. CLIProxy wraps upstream failures in a value implementing the exported
// cliproxyexecutor.StatusError interface; err.Error() is the upstream body.
// Returns status 0 when no status is carried (e.g. a scanner error).
func StatusFromExecErr(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	var se cliproxyexecutor.StatusError
	if errors.As(err, &se) {
		return se.StatusCode(), err.Error()
	}
	return 0, err.Error()
}

func ClassifyExecError(err error) ErrorPayload {
	status, body := StatusFromExecErr(err)
	code := ojson.Get(body, "error.code").String()
	disposition := Classify(status, body)
	if cliproxyexecutor.IsUpstreamWebsocketReplayRequired(err) ||
		code == "previous_response_not_found" ||
		code == "websocket_connection_limit_reached" {
		disposition = DispositionSessionLost
	}
	return ErrorPayload{
		Disposition:    disposition,
		UpstreamStatus: status,
		Code:           code,
		Message:        truncate(body, 2000),
	}
}

// ClassifyRefreshError classifies a failed token refresh. The refresh path
// returns a string-only error (no typed status): a permanent refusal
// (invalid_grant / refresh_token_reused / HTTP 400 / 401) means the grant is dead
// -> revoked; anything else is transient -> upstream_error (status untouched).
func ClassifyRefreshError(err error) ErrorPayload {
	s := strings.ToLower(err.Error())
	permanent := strings.Contains(s, "invalid_grant") ||
		strings.Contains(s, "refresh_token_reused") ||
		strings.Contains(s, "token refresh rejected:") ||
		strings.Contains(s, "status 400") ||
		strings.Contains(s, "status 401")
	d := DispositionUpstreamError
	if permanent {
		d = DispositionRevoked
	}
	return ErrorPayload{Disposition: d, Code: "refresh_failed", Message: truncate(err.Error(), 2000)}
}

func FormatErrorEvent(p ErrorPayload) []byte {
	data, _ := json.Marshal(p)
	out := append([]byte("event: tokenswim.error\ndata: "), data...)
	return append(out, '\n', '\n')
}

func (p ErrorPayload) HTTPStatus() int {
	switch p.Disposition {
	case DispositionInvalid, DispositionExpired:
		return 401
	case DispositionRevoked:
		return 403
	case DispositionRateLimited:
		return 429
	case DispositionSessionLost:
		return 409
	default:
		return 502
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

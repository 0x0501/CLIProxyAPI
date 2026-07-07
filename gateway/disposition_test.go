package gateway

import (
	"bytes"
	"errors"
	"testing"
)

func TestClassifyStatus(t *testing.T) {
	cases := map[int]Disposition{
		401: DispositionInvalid,
		403: DispositionRevoked,
		429: DispositionRateLimited,
		500: DispositionUpstreamError,
		503: DispositionUpstreamError,
	}
	for status, want := range cases {
		if got := Classify(status, ""); got != want {
			t.Fatalf("status %d: got %s want %s", status, got, want)
		}
	}
}

func TestClassify403DeactivatedIsRevoked(t *testing.T) {
	if got := Classify(403, `{"error":{"code":"account_deactivated"}}`); got != DispositionRevoked {
		t.Fatalf("got %s", got)
	}
}

func TestFormatErrorEvent(t *testing.T) {
	got := FormatErrorEvent(ErrorPayload{Disposition: DispositionRevoked, UpstreamStatus: 403, Code: "account_deactivated", Message: "banned"})
	if !bytes.HasPrefix(got, []byte("event: tokenswim.error\n")) || !bytes.HasSuffix(got, []byte("\n\n")) {
		t.Fatalf("bad framing: %q", got)
	}
	if !bytes.Contains(got, []byte(`"disposition":"revoked"`)) {
		t.Fatalf("missing disposition: %q", got)
	}
}

// fakeStatusErr implements the exported cliproxyexecutor.StatusError interface.
type fakeStatusErr struct {
	code int
	msg  string
}

func (e fakeStatusErr) Error() string   { return e.msg }
func (e fakeStatusErr) StatusCode() int { return e.code }

func TestStatusFromExecErrReadsStatusError(t *testing.T) {
	status, body := StatusFromExecErr(fakeStatusErr{code: 403, msg: `{"error":{"code":"account_deactivated"}}`})
	if status != 403 || body == "" {
		t.Fatalf("got status=%d body=%q", status, body)
	}
	// A plain error carries no status.
	if s, _ := StatusFromExecErr(errors.New("boom")); s != 0 {
		t.Fatalf("plain error must yield status 0, got %d", s)
	}
}

func TestClassifyExecErrorSetsCodeFromBody(t *testing.T) {
	p := ClassifyExecError(fakeStatusErr{code: 403, msg: `{"error":{"code":"account_deactivated"}}`})
	if p.Disposition != DispositionRevoked || p.UpstreamStatus != 403 || p.Code != "account_deactivated" {
		t.Fatalf("bad payload: %+v", p)
	}
}

func TestClassifyRefreshErrorDeadGrantIsRevoked(t *testing.T) {
	dead := ClassifyRefreshError(errors.New(`token refresh failed after 3 attempts: token refresh failed with status 400: {"error":"invalid_grant"}`))
	if dead.Disposition != DispositionRevoked {
		t.Fatalf("invalid_grant must be revoked, got %s", dead.Disposition)
	}
	transient := ClassifyRefreshError(errors.New("token refresh failed with status 503: bad gateway"))
	if transient.Disposition != DispositionUpstreamError {
		t.Fatalf("5xx refresh must be transient, got %s", transient.Disposition)
	}
}

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

func TestClassify2xxIsOK(t *testing.T) {
	for _, status := range []int{200, 204} {
		if got := Classify(status, ""); got != DispositionOK {
			t.Fatalf("status %d: got %s want %s", status, got, DispositionOK)
		}
	}
}

func TestClassifyOther4xxIsTransientNotInvalid(t *testing.T) {
	for _, status := range []int{400, 404} {
		if got := Classify(status, ""); got != DispositionUpstreamError {
			t.Fatalf("status %d: got %s want %s (must not be credential-fatal)", status, got, DispositionUpstreamError)
		}
	}
}

func TestClassify402IsInvalid(t *testing.T) {
	if got := Classify(402, ""); got != DispositionInvalid {
		t.Fatalf("got %s want %s", got, DispositionInvalid)
	}
}

func TestHTTPStatusDispatchesOnDisposition(t *testing.T) {
	cases := map[Disposition]int{
		DispositionRevoked:       403,
		DispositionInvalid:       401,
		DispositionRateLimited:   429,
		DispositionUpstreamError: 502,
	}
	for d, want := range cases {
		if got := (ErrorPayload{Disposition: d}).HTTPStatus(); got != want {
			t.Fatalf("disposition %s: got %d want %d", d, got, want)
		}
	}

	// Headline dead-grant scenario: refresh failure carries no UpstreamStatus,
	// so HTTPStatus must switch on Disposition, not UpstreamStatus.
	dead := ClassifyRefreshError(errors.New(`token refresh failed with status 400: {"error":"invalid_grant"}`))
	if got := dead.HTTPStatus(); got != 403 {
		t.Fatalf("dead grant: got %d want 403 (payload=%+v)", got, dead)
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

func TestStatusFromExecErrPreservesMessageForNonStatusError(t *testing.T) {
	status, msg := StatusFromExecErr(errors.New("network boom"))
	if status != 0 || msg != "network boom" {
		t.Fatalf("got status=%d msg=%q, want status=0 msg=%q", status, msg, "network boom")
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

package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type recordedSessions struct {
	*UpstreamSessions
	clock  time.Time
	closed []string
}

func newRecordedSessions(limit int) *recordedSessions {
	r := &recordedSessions{clock: time.Unix(1_000_000, 0)}
	r.UpstreamSessions = &UpstreamSessions{
		limit:    limit,
		idle:     10 * time.Minute,
		now:      func() time.Time { return r.clock },
		close:    func(id string) { r.closed = append(r.closed, id) },
		sessions: map[string]*upstreamSession{},
	}
	return r
}

func TestUpstreamSessionsSweepClosesOnlyIdleSessions(t *testing.T) {
	s := newRecordedSessions(10)
	s.Begin("idle")()
	busyEnd := s.Begin("busy")
	s.clock = s.clock.Add(11 * time.Minute)
	s.Begin("fresh")()
	s.Sweep()
	if fmt.Sprint(s.closed) != "[idle]" {
		t.Fatalf("closed = %v, want [idle]", s.closed)
	}
	busyEnd()
	s.Close("busy")
	s.Close("unknown")
	if fmt.Sprint(s.closed) != "[idle busy]" {
		t.Fatalf("closed = %v, want [idle busy]", s.closed)
	}
}

func TestUpstreamSessionsCloseSkipsActiveSession(t *testing.T) {
	s := newRecordedSessions(10)
	end := s.Begin("a")
	s.Close("a")
	if len(s.closed) != 0 {
		t.Fatalf("closed an active session: %v", s.closed)
	}
	end()
}

func TestUpstreamSessionsEvictLeastRecentlyUsedIdleBeyondCap(t *testing.T) {
	s := newRecordedSessions(2)
	s.Begin("oldest")()
	s.clock = s.clock.Add(time.Second)
	end := s.Begin("active")
	s.clock = s.clock.Add(time.Second)
	s.Begin("newer")()
	s.clock = s.clock.Add(time.Second)
	s.Begin("newest")()
	if fmt.Sprint(s.closed) != "[oldest newer]" {
		t.Fatalf("closed = %v, want [oldest newer]", s.closed)
	}
	end()
}

func TestClassifyReplayRequiredIsSessionLost(t *testing.T) {
	for _, err := range []error{
		cliproxyexecutor.NewUpstreamWebsocketReplayRequiredError(),
		fmt.Errorf("wrapped: %w", cliproxyexecutor.NewUpstreamWebsocketReplayRequiredError()),
		errors.New(`{"error":{"code":"previous_response_not_found","message":"gone"}}`),
	} {
		p := ClassifyExecError(err)
		if p.Disposition != DispositionSessionLost || p.HTTPStatus() != http.StatusConflict {
			t.Fatalf("%v: got %s/%d, want session_lost/409", err, p.Disposition, p.HTTPStatus())
		}
	}
}

func TestDecodeEnvelopeContinuationRequiresSession(t *testing.T) {
	body := `{"provider":"codex","credential":{"access_token":"a"},"request":{"model":"m"},"continuation":true}`
	if _, err := DecodeEnvelope(strings.NewReader(body)); err == nil {
		t.Fatal("continuation without session must be rejected")
	}
}

// fakeUpstream is a Codex Responses WebSocket endpoint that answers every
// response.create on a connection with created + completed.
type fakeUpstream struct {
	dials    atomic.Int32
	mu       sync.Mutex
	payloads [][]byte
	// failWith, when set, answers the next request with this event instead.
	failWith string
}

func (f *fakeUpstream) handler(t *testing.T) http.HandlerFunc {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		f.dials.Add(1)
		defer func() { _ = conn.Close() }()
		for {
			_, payload, errRead := conn.ReadMessage()
			if errRead != nil {
				return
			}
			f.mu.Lock()
			f.payloads = append(f.payloads, bytes.Clone(payload))
			n := len(f.payloads)
			fail := f.failWith
			f.failWith = ""
			f.mu.Unlock()
			if fail != "" {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_x"}}`))
				_ = conn.WriteMessage(websocket.TextMessage, []byte(fail))
				continue
			}
			id := fmt.Sprintf("resp_%d", n)
			for _, event := range []string{
				`{"type":"response.created","response":{"id":"` + id + `"}}`,
				`{"type":"response.completed","response":{"id":"` + id + `","output":[],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`,
			} {
				if errWrite := conn.WriteMessage(websocket.TextMessage, []byte(event)); errWrite != nil {
					return
				}
			}
		}
	}
}

func (f *fakeUpstream) payload(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.payloads[i]
}

type sessionHarness struct {
	upstream *fakeUpstream
	sessions *UpstreamSessions
	invoke   func(Envelope) *httptest.ResponseRecorder
	envelope func(request string, continuation bool) Envelope
}

func newSessionHarness(t *testing.T) *sessionHarness {
	upstream := &fakeUpstream{}
	server := httptest.NewServer(upstream.handler(t))
	t.Cleanup(server.Close)

	cfg := &config.Config{}
	sessions := NewUpstreamSessions(cfg)
	provider, _ := Lookup("codex")
	invoke := func(env Envelope) *httptest.ResponseRecorder {
		auth := &cliproxyauth.Auth{
			Provider:   "codex",
			Attributes: map[string]string{"api_key": "sk-test", "base_url": server.URL},
			Metadata:   map[string]any{"expired": time.Now().Add(time.Hour).Format(time.RFC3339)},
		}
		rec := httptest.NewRecorder()
		serve(rec, httptest.NewRequest(http.MethodPost, "/invoke", nil), cfg, sessions, provider, env, auth)
		return rec
	}
	envelope := func(request string, continuation bool) Envelope {
		return Envelope{
			Provider:     "codex",
			Credential:   map[string]any{"access_token": "a"},
			Request:      json.RawMessage(request),
			Format:       "openai-response",
			Session:      "socket-1:auth-1",
			Continuation: continuation,
		}
	}
	return &sessionHarness{upstream: upstream, sessions: sessions, invoke: invoke, envelope: envelope}
}

func TestServeKeepsOneUpstreamSocketPerSession(t *testing.T) {
	h := newSessionHarness(t)
	upstream, sessions, invoke, envelope := h.upstream, h.sessions, h.invoke, h.envelope

	first := invoke(envelope(`{"model":"gpt-5.4","stream":true,"input":[{"role":"user","content":"hi"}]}`, false))
	if first.Code != http.StatusOK || first.Header().Get(UpstreamSessionHeader) != "1" {
		t.Fatalf("first call: %d %q %s", first.Code, first.Header().Get(UpstreamSessionHeader), first.Body)
	}
	if !strings.Contains(first.Body.String(), "response.completed") ||
		!strings.Contains(first.Body.String(), "event: tokenswim.usage") {
		t.Fatalf("first call stream lacks completion or usage frame:\n%s", first.Body)
	}

	second := invoke(envelope(`{"model":"gpt-5.4","stream":true,"previous_response_id":"resp_1","input":[{"role":"user","content":"more"}]}`, true))
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "resp_2") {
		t.Fatalf("continuation: %d %s", second.Code, second.Body)
	}
	if got := upstream.dials.Load(); got != 1 {
		t.Fatalf("upstream dials = %d, want 1 (socket reused)", got)
	}
	sent := upstream.payload(1)
	if ojson.GetBytes(sent, "previous_response_id").String() != "resp_1" ||
		len(ojson.GetBytes(sent, "input").Array()) != 1 {
		t.Fatalf("continuation was not sent as a delta: %s", sent)
	}

	// Once the session is closed a continuation must fail, never dial a fresh
	// socket or fall back to HTTP without the earlier context.
	sessions.Close("socket-1:auth-1")
	lost := invoke(envelope(`{"model":"gpt-5.4","stream":true,"previous_response_id":"resp_2","input":[]}`, true))
	if lost.Code != http.StatusConflict ||
		ojson.GetBytes(lost.Body.Bytes(), "error.disposition").String() != string(DispositionSessionLost) {
		t.Fatalf("lost session: %d %s", lost.Code, lost.Body)
	}
	if got := upstream.dials.Load(); got != 1 {
		t.Fatalf("a lost continuation dialed upstream again (%d dials)", got)
	}
}

func TestServeReportsUpstreamFirstEventErrorsBeforeTheStream(t *testing.T) {
	h := newSessionHarness(t)
	ok := h.invoke(h.envelope(`{"model":"gpt-5.4","stream":true,"input":[{"role":"user","content":"hi"}]}`, false))
	if ok.Code != http.StatusOK {
		t.Fatalf("first call: %d %s", ok.Code, ok.Body)
	}

	// The upstream no longer holds the response: the caller must resend in full.
	h.upstream.failWith = `{"type":"error","status":400,"error":{"type":"invalid_request_error","code":"previous_response_not_found","message":"Previous response not found."}}`
	lost := h.invoke(h.envelope(`{"model":"gpt-5.4","stream":true,"previous_response_id":"resp_1","input":[]}`, true))
	if lost.Code != http.StatusConflict ||
		ojson.GetBytes(lost.Body.Bytes(), "error.disposition").String() != string(DispositionSessionLost) {
		t.Fatalf("previous_response_not_found: %d %s", lost.Code, lost.Body)
	}

	// A credential limit must stay a pre-stream refusal so web can fail over.
	h.upstream.failWith = `{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`
	limited := h.invoke(h.envelope(`{"model":"gpt-5.4","stream":true,"input":[{"role":"user","content":"hi"}]}`, false))
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("usage limit: %d %s", limited.Code, limited.Body)
	}
}

func TestDecodeEnvelopeIgnoresUnknownFields(t *testing.T) {
	// Web may send fields an older image does not know (session, continuation):
	// they must be ignored, never refused, so either side can roll out first.
	body := `{"provider":"codex","credential":{"access_token":"a"},"request":{"model":"m"},"bogus":1}`
	if _, err := DecodeEnvelope(strings.NewReader(body)); err != nil {
		t.Fatalf("unknown field refused: %v", err)
	}
}

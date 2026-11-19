package gateway

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	codexexec "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// UpstreamSessionHeader tells the caller the call ran on a named upstream
// WebSocket session, so a later continuation may be sent to this node.
const UpstreamSessionHeader = "X-Tokenswim-Upstream-Session"

const (
	defaultSessionLimit = 512
	defaultSessionIdle  = 15 * time.Minute
)

// UpstreamSessions owns the Codex WebSocket executor's named sessions. The
// executor keeps a session until it is closed, so the gateway closes them on
// request, after an idle period, and oldest-first beyond a fixed cap.
type UpstreamSessions struct {
	exec  *codexexec.CodexWebsocketsExecutor
	limit int
	idle  time.Duration
	now   func() time.Time
	close func(id string)

	mu       sync.Mutex
	sessions map[string]*upstreamSession
}

type upstreamSession struct {
	active   int
	lastUsed time.Time
}

func NewUpstreamSessions(cfg *config.Config) *UpstreamSessions {
	exec := codexexec.NewCodexWebsocketsExecutor(cfg)
	return &UpstreamSessions{
		exec:     exec,
		limit:    defaultSessionLimit,
		idle:     defaultSessionIdle,
		now:      time.Now,
		close:    exec.CloseExecutionSession,
		sessions: map[string]*upstreamSession{},
	}
}

// Begin marks a call on session id as active; the returned func ends it.
func (s *UpstreamSessions) Begin(id string) func() {
	s.mu.Lock()
	entry := s.sessions[id]
	if entry == nil {
		entry = &upstreamSession{}
		s.sessions[id] = entry
	}
	entry.active++
	entry.lastUsed = s.now()
	s.evictLocked()
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		entry.active--
		entry.lastUsed = s.now()
		s.mu.Unlock()
	}
}

// Close drops session id unless a call is still running on it.
func (s *UpstreamSessions) Close(id string) {
	s.mu.Lock()
	entry := s.sessions[id]
	if entry == nil || entry.active > 0 {
		s.mu.Unlock()
		return
	}
	delete(s.sessions, id)
	// Closed under the lock: a Begin for the same id must not start a call on
	// an executor session that a pending close is about to kill.
	s.close(id)
	s.mu.Unlock()
}

// Sweep closes sessions idle longer than the idle period.
func (s *UpstreamSessions) Sweep() {
	cutoff := s.now().Add(-s.idle)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, entry := range s.sessions {
		if entry.active == 0 && entry.lastUsed.Before(cutoff) {
			delete(s.sessions, id)
			s.close(id)
		}
	}
}

// Run sweeps until ctx ends.
func (s *UpstreamSessions) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep()
		}
	}
}

// evictLocked closes the least recently used idle sessions beyond the cap.
func (s *UpstreamSessions) evictLocked() {
	excess := len(s.sessions) - s.limit
	if excess <= 0 {
		return
	}
	idle := make([]string, 0, len(s.sessions))
	for id, entry := range s.sessions {
		if entry.active == 0 {
			idle = append(idle, id)
		}
	}
	sort.Slice(idle, func(i, j int) bool {
		return s.sessions[idle[i]].lastUsed.Before(s.sessions[idle[j]].lastUsed)
	})
	if excess > len(idle) {
		excess = len(idle)
	}
	for _, id := range idle[:excess] {
		delete(s.sessions, id)
		s.close(id)
	}
}

// CloseHandler serves POST /session/close {"session": "..."}; best effort.
func CloseHandler(sessions *UpstreamSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Session string `json:"session"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Session == "" {
			http.Error(w, "missing session", http.StatusBadRequest)
			return
		}
		sessions.Close(body.Session)
		w.WriteHeader(http.StatusNoContent)
	}
}

// splitBootstrapError holds a WebSocket call's status line until the stream
// shows model output. Over HTTP an upstream refusal is a status code; over the
// WebSocket it is an error event, possibly after response.created. An error
// before any output is returned instead of a stream, so the caller still sees a
// pre-stream refusal it can fail over (or, for session_lost, resend in full).
func splitBootstrapError(chunks <-chan cliproxyexecutor.StreamChunk) (<-chan cliproxyexecutor.StreamChunk, error) {
	var queued []cliproxyexecutor.StreamChunk
	for {
		chunk, ok := <-chunks
		if !ok {
			return replay(queued, nil), nil
		}
		if chunk.Err != nil {
			go discard(chunks)
			return nil, chunk.Err
		}
		queued = append(queued, chunk)
		if !isLifecycleChunk(chunk.Payload) {
			return replay(queued, chunks), nil
		}
	}
}

// replay yields queued chunks, then the remaining live ones (if any).
func replay(queued []cliproxyexecutor.StreamChunk, rest <-chan cliproxyexecutor.StreamChunk) <-chan cliproxyexecutor.StreamChunk {
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		for _, chunk := range queued {
			out <- chunk
		}
		if rest != nil {
			for chunk := range rest {
				out <- chunk
			}
		}
	}()
	return out
}

// isLifecycleChunk reports whether a stream line carries no model output:
// blank lines, event: names and response.created / response.in_progress data.
func isLifecycleChunk(line []byte) bool {
	text := strings.TrimSpace(string(line))
	if text == "" || strings.HasPrefix(text, "event:") {
		return true
	}
	data, ok := strings.CutPrefix(text, "data:")
	if !ok {
		return false
	}
	switch ojson.Get(strings.TrimSpace(data), "type").String() {
	case "response.created", "response.in_progress":
		return true
	}
	return false
}

func discard(chunks <-chan cliproxyexecutor.StreamChunk) {
	for range chunks {
	}
}

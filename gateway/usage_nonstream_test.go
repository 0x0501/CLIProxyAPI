package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// An Anthropic non-stream body the way the codex->claude translator produces it:
// input excludes cache read, and there is no wire slot for cache write or
// reasoning. Re-parsing this is the only source the non-stream path had before
// ADR 0019.
const claudeNonStreamBody = `{"id":"msg_1","type":"message","role":"assistant","model":"gpt-5.6-terra","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":600,"output_tokens":50,"cache_read_input_tokens":400}}`

// fakeExecutor stands in for a provider executor: it publishes a raw-upstream
// usage record on the context the handler gave it, then returns a translated
// client-facing body.
type fakeExecutor struct {
	payload []byte
	model   string
	publish *usage.Detail
}

func (f *fakeExecutor) Identifier() string { return "fake" }

func (f *fakeExecutor) Execute(ctx context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	if f.publish != nil {
		usage.PublishRecord(ctx, usage.Record{Model: f.model, Detail: *f.publish})
	}
	return cliproxyexecutor.Response{Payload: f.payload}, nil
}

func (f *fakeExecutor) ExecuteStream(context.Context, *cliproxyauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, errors.New("fakeExecutor: streaming not used by this test")
}

func (f *fakeExecutor) Refresh(context.Context, *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	return nil, nil
}

func (f *fakeExecutor) CountTokens(context.Context, *cliproxyauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (f *fakeExecutor) HttpRequest(context.Context, *cliproxyauth.Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("fakeExecutor: HttpRequest not used by this test")
}

func registerFakeProvider(t *testing.T, exec *fakeExecutor) {
	t.Helper()
	Registry["fake"] = Provider{
		NewExecutor: func(*config.Config) cliproxyauth.ProviderExecutor { return exec },
	}
	t.Cleanup(func() { delete(Registry, "fake") })
}

func nonStreamUsageHeader(t *testing.T, exec *fakeExecutor) (UsagePayload, bool) {
	t.Helper()
	registerFakeProvider(t, exec)
	body := `{"provider":"fake","format":"claude","credential":{"access_token":"a"},"request":{"model":"` + fixtureModel + `"}}`
	rec := httptest.NewRecorder()
	Handler(&config.Config{})(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("handler returned %d: %s", rec.Code, rec.Body.String())
	}
	raw := rec.Header().Get("X-Tokenswim-Usage")
	if raw == "" {
		return UsagePayload{}, false
	}
	var u UsagePayload
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("X-Tokenswim-Usage is not valid JSON (%v): %s", err, raw)
	}
	return u, true
}

// The non-stream usage header must follow raw upstream too — same rule as the
// streaming frame, so a consumer's bill does not depend on whether they asked
// for SSE (ADR 0019).
func TestHandlerNonStreamUsageHeaderFollowsRawUpstream(t *testing.T) {
	raw := rawFixtureDetail(t)
	u, ok := nonStreamUsageHeader(t, &fakeExecutor{
		payload: []byte(claudeNonStreamBody),
		model:   fixtureModel,
		publish: &raw,
	})
	if !ok {
		t.Fatal("no X-Tokenswim-Usage header emitted")
	}
	assertFixtureBuckets(t, u)
}

func TestHandlerNonStreamUsageHeaderFallsBackToReparse(t *testing.T) {
	u, ok := nonStreamUsageHeader(t, &fakeExecutor{
		payload: []byte(claudeNonStreamBody),
		model:   fixtureModel,
	})
	if !ok {
		t.Fatal("no X-Tokenswim-Usage header emitted")
	}
	if u.CacheReadTokens != 400 || u.OutputTokens != 50 {
		t.Fatalf("re-parse lost the buckets claude does carry: %+v", u)
	}
	if u.CacheCreationTokens != 0 || u.ReasoningTokens != 0 {
		t.Fatalf("claude non-stream body cannot carry cache write or reasoning: %+v", u)
	}
}

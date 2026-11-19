package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestCodexGatewayNormalizesLiteralClassBrackets(t *testing.T) {
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
	}))
	defer upstream.Close()
	exec := executor.NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": upstream.URL}, Metadata: map[string]any{"access_token": "test"}}
	result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{Model: "gpt-6.1-sol", Payload: []byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"Reply OK."}],"tools":[{"type":"function","function":{"name":"start","parameters":{"type":"object","properties":{"path":{"type":"string","pattern":"^(?:\\.|[^/\\\\*?[\\]{}\\u0000-\\u001f]+(?:\\/[^/\\\\*?[\\]{}\\u0000-\\u001f]+)*)$"}},"required":["path"],"additionalProperties":false}}}],"stream":true}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	for range result.Chunks {
	}
	want := `^(?:\.|[^/\\*?\[\]{}\u0000-\u001f]+(?:\/[^/\\*?\[\]{}\u0000-\u001f]+)*)$`
	got := ojson.GetBytesNoCopy(gotBody, "tools.0.parameters.properties.path.pattern").String()
	if got != want {
		t.Fatalf("pattern changed: got %q, want %q; body=%s", got, want, gotBody)
	}
}

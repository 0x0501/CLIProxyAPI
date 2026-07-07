package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsHandlerReturnsOpenAIList(t *testing.T) {
	rec := httptest.NewRecorder()
	ModelsHandler(rec, httptest.NewRequest(http.MethodGet, "/models", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("bad response: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if body.Object != "list" || len(body.Data) == 0 {
		t.Fatalf("expected non-empty list, got %+v", body)
	}
	for _, m := range body.Data {
		if m.ID == "" || m.Object != "model" {
			t.Fatalf("model must have id + object=model: %+v", m)
		}
	}
}

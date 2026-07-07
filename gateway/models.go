package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelsHandler returns the codex model catalog in OpenAI's {object:"list"} shape.
func ModelsHandler(w http.ResponseWriter, _ *http.Request) {
	models := registry.GetCodexProModels()
	data := make([]openAIModel, 0, len(models))
	for _, m := range models {
		owner := m.OwnedBy
		if owner == "" {
			owner = "tokenswim"
		}
		data = append(data, openAIModel{ID: m.ID, Object: "model", Created: m.Created, OwnedBy: owner})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

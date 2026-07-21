package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Family  string `json:"family"`
}

// ModelsHandler returns the configured text-capable upstream catalog in OpenAI's
// {object:"list"} shape, with Family for the maintenance model-sync UI.
func ModelsHandler(w http.ResponseWriter, _ *http.Request) {
	data := make([]openAIModel, 0, len(registry.GetCodexProModels())+len(registry.GetXAIModels()))
	appendModels := func(models []*registry.ModelInfo, family string) {
		for _, m := range models {
			if family == "xai" && strings.HasPrefix(strings.ToLower(m.ID), "grok-imagine-") {
				continue
			}
			owner := m.OwnedBy
			if owner == "" {
				owner = family
			}
			data = append(data, openAIModel{ID: m.ID, Object: "model", Created: m.Created, OwnedBy: owner, Family: family})
		}
	}
	appendModels(registry.GetCodexProModels(), "openai")
	appendModels(registry.GetXAIModels(), "xai")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

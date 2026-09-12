package api

import (
	_ "embed"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/server/biz"
)

// Codex replaces whole ModelInfo entries, including instructions and tool settings.
// Keep the versioned upstream descriptors intact instead of inventing capabilities.
//
//go:embed codexmodels/models.json
var codexModelsJSON []byte

//go:embed codexmodels/prompt.md
var codexFallbackInstructions string

var codexModelCatalog = sync.OnceValues(func() (map[string]map[string]json.RawMessage, error) {
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(codexModelsJSON, &catalog); err != nil {
		return nil, err
	}
	models := make(map[string]map[string]json.RawMessage, len(catalog.Models))
	for _, raw := range catalog.Models {
		var slug string
		if err := json.Unmarshal(raw["slug"], &slug); err != nil {
			return nil, err
		}
		models[slug] = raw
	}
	return models, nil
})

// Use the client's longest-prefix and single provider-namespace lookup so dated
// and prefixed IDs retain the same instructions as before the remote refresh.
func findCodexModel(id string, catalog map[string]map[string]json.RawMessage) map[string]json.RawMessage {
	best := ""
	for slug := range catalog {
		if strings.HasPrefix(id, slug) && len(slug) > len(best) {
			best = slug
		}
	}
	if best != "" {
		return catalog[best]
	}
	namespace, suffix, ok := strings.Cut(id, "/")
	if !ok || namespace == "" || strings.Contains(suffix, "/") {
		return nil
	}
	for _, c := range namespace {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return nil
		}
	}
	return findCodexModel(suffix, catalog)
}

func (handlers *OpenAIHandlers) listCodexModels(c *gin.Context, visible []biz.ModelFacade) {
	ctx := c.Request.Context()
	requestID, _ := contexts.GetRequestID(ctx)
	catalog, err := codexModelCatalog()
	if err != nil {
		handlers.writeOpenAIInternalError(c, requestID, err)
		return
	}

	// Only supplement already-authorized model IDs. The catalog must never make
	// other projects' or keys' models discoverable.
	unknownIDs := make([]string, 0)
	for _, m := range visible {
		if findCodexModel(m.ID, catalog) == nil {
			unknownIDs = append(unknownIDs, m.ID)
		}
	}
	configured := map[string]*ent.Model{}
	if len(unknownIDs) > 0 {
		models, err := handlers.EntClient.Model.Query().Where(model.StatusEQ(model.StatusEnabled), model.ModelIDIn(unknownIDs...)).All(ctx)
		if err != nil {
			handlers.writeOpenAIInternalError(c, requestID, err)
			return
		}
		for _, m := range models {
			configured[m.ModelID] = m
		}
	}

	models := make([]json.RawMessage, 0, len(visible))
	for _, m := range visible {
		var descriptor any
		if known := findCodexModel(m.ID, catalog); known != nil {
			copy := maps.Clone(known)
			copy["slug"], _ = json.Marshal(m.ID)
			// The gateway's enabled, key-scoped models define picker availability.
			copy["visibility"] = json.RawMessage(`"list"`)
			descriptor = copy
		} else {
			descriptor = codexFallbackModel(m.ID, configured[m.ID])
		}
		raw, err := json.Marshal(descriptor)
		if err != nil {
			handlers.writeOpenAIInternalError(c, requestID, err)
			return
		}
		models = append(models, raw)
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"models": models})
}

// Match Codex 0.153.4 model_info_from_slug for unknown IDs, with an explicit
// configured context limit when available. Do not borrow another model's prompt,
// reasoning levels, service tiers or tools beyond the client's own lookup rules.
func codexFallbackModel(id string, configured *ent.Model) map[string]any {
	name, contextWindow := id, 272000
	if configured != nil {
		name = configured.Name
		if configured.ModelCard != nil && configured.ModelCard.Limit.Context > 0 {
			contextWindow = configured.ModelCard.Limit.Context
		}
	}
	return map[string]any{
		"slug": id, "display_name": name, "description": nil,
		"supported_reasoning_levels": []any{}, "shell_type": "unified_exec",
		"visibility": "list", "supported_in_api": true, "priority": 99,
		"model_messages":                    map[string]any{"instructions_template": codexFallbackInstructions},
		"include_skills_usage_instructions": false, "include_plugin_usage_instructions": false,
		"include_apps_usage_instructions":      false,
		"supports_reasoning_summary_parameter": true, "default_reasoning_summary": "auto",
		"support_verbosity": false, "truncation_policy": map[string]any{"mode": "bytes", "limit": 10000},
		"context_window": contextWindow, "max_context_window": contextWindow,
		"effective_context_window_percent": 95,
		"experimental_supported_tools":     []string{}, "input_modalities": []string{"text", "image"},
	}
}

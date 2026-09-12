package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/model"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestOpenAIHandlers_ListModels_CodexCatalog(t *testing.T) {
	client, channelSvc, _, router, ctx := setupOpenAIRetrieveTest(t)
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpenaiResponses).SetName("Codex models").
		SetBaseURL("https://example.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).
		SetSupportedModels([]string{"gpt-6-astra", "custom-model", "hidden-model", "openai/gpt-5.4-2026-03-05"}).
		SetDefaultTestModel("gpt-6-astra").
		SetStatus(channel.StatusEnabled).Save(ctx)
	require.NoError(t, err)
	channelSvc.SetEnabledChannelsForTest([]*biz.Channel{{Channel: ch}})
	_, err = client.Model.Create().SetDeveloper("custom").SetModelID("custom-model").
		SetName("Custom model").SetIcon("custom").SetGroup("custom").SetType(model.TypeChat).SetStatus(model.StatusEnabled).
		SetModelCard(&objects.ModelCard{Limit: objects.ModelCardLimit{Context: 32768}}).
		SetSettings(&objects.ModelSettings{Associations: []*objects.ModelAssociation{{
			Type: "channel_model", ChannelModel: &objects.ChannelModelAssociation{ChannelID: ch.ID, ModelID: "custom-model"},
		}}}).Save(ctx)
	require.NoError(t, err)

	key := &ent.APIKey{Profiles: &objects.APIKeyProfiles{
		ActiveProfile: "limited", Profiles: []objects.APIKeyProfile{{Name: "limited", ModelIDs: []string{"gpt-6-astra", "custom-model", "openai/gpt-5.4-2026-03-05"}}},
	}}
	// Keep the same API key context for ordinary and Codex discovery requests.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r.WithContext(contexts.WithAPIKey(r.Context(), key)))
	})

	for _, path := range []string{"/v1/models?client_version=0.153.4", "/v1/models?client_version=0.153.4&include=all"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Models []map[string]any `json:"models"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Len(t, response.Models, 3, "Codex must receive a models array filtered by the API key")
			byID := map[string]map[string]any{}
			for _, m := range response.Models {
				byID[m["slug"].(string)] = m
				for _, field := range []string{"display_name", "supported_reasoning_levels", "shell_type", "visibility", "supported_in_api", "priority", "support_verbosity", "truncation_policy", "experimental_supported_tools"} {
					require.Contains(t, m, field, "required by Codex ModelInfo")
					require.NotNil(t, m[field])
				}
				require.NotEmpty(t, m["model_messages"].(map[string]any)["instructions_template"], "catalog replacement must not erase Codex instructions")
			}
			require.NotContains(t, byID, "hidden-model")
			require.Equal(t, float64(272000), byID["gpt-6-astra"]["context_window"])
			require.Equal(t, float64(872000), byID["gpt-6-astra"]["max_context_window"])
			require.Equal(t, float64(32768), byID["custom-model"]["context_window"])
			require.Equal(t, "list", byID["openai/gpt-5.4-2026-03-05"]["visibility"])
			require.Equal(t, float64(1000000), byID["openai/gpt-5.4-2026-03-05"]["max_context_window"])
			catalog, err := codexModelCatalog()
			require.NoError(t, err)
			for _, field := range []string{"model_messages", "supported_reasoning_levels", "service_tiers", "tool_mode", "use_responses_lite"} {
				got, err := json.Marshal(byID["gpt-6-astra"][field])
				require.NoError(t, err)
				require.JSONEq(t, string(catalog["gpt-6-astra"][field]), string(got), "preserve native Codex metadata: %s", field)
			}
		})
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var ordinary map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ordinary))
	require.Contains(t, ordinary, "data")
	require.NotContains(t, ordinary, "models")

	key.Profiles.Profiles[0].ModelIDs = []string{"unavailable"}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.153.4", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"models":[]}`, w.Body.String())
}

func TestCodexCatalog_ClientMetadataLookup(t *testing.T) {
	catalog, err := codexModelCatalog()
	require.NoError(t, err)
	for _, id := range []string{"gpt-5.4-mini-2026-03-17", "provider_1/gpt-5.4-mini", "gpt-5.4-mini"} {
		require.JSONEq(t, `"gpt-5.4-mini"`, string(findCodexModel(id, catalog)["slug"]))
	}
	for _, id := range []string{"unknown", "namespace/another/gpt-5.4", "not.a.provider/gpt-5.4", "/gpt-5.4"} {
		require.Nil(t, findCodexModel(id, catalog))
	}
	// Serving a namespaced/visible clone must not mutate shared snapshot data.
	require.JSONEq(t, `"hide"`, string(catalog["gpt-5.4"]["visibility"]))
}

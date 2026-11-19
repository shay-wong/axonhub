package gql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestContextPriceGraphQLRoundTrip(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	ch := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Context price").
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "test"}).
		SetSupportedModels([]string{"gpt-6-astra"}).
		SetDefaultTestModel("gpt-6-astra").SaveX(ctx)
	server := handler.NewDefaultServer(NewExecutableSchema(Config{
		Resolvers: &Resolver{channelService: biz.NewChannelServiceForTest(client)},
	}))

	// Every change must survive GraphQL input/output, JSON storage and price
	// versioning. Equal configurations must not create a redundant version.
	for _, step := range []struct {
		bound    int64
		rate     int
		versions int
	}{
		{272000, 75, 1},
		{272000, 75, 1},
		{300000, 75, 2},
		{300000, 80, 3},
	} {
		query := fmt.Sprintf(`mutation {
			saveChannelModelPrices(channelId: "gid://axonhub/Channel/%d", input: [{
				modelId: "gpt-6-astra", price: {items: [{itemCode: completion_tokens, pricing: {
					mode: usage_context, usageTiered: {tiers: [
						{upTo: %d, pricePerUnit: "50"}, {pricePerUnit: "%d"}
					]}
				}}]}
			}]) {price {items {pricing {mode usageTiered {tiers {upTo pricePerUnit}}}}}}
		}`, ch.ID, step.bound, step.rate)
		payload, err := json.Marshal(map[string]string{"query": query})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(payload)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		require.Equal(t, http.StatusOK, response.Code)
		require.NotContains(t, response.Body.String(), `"errors"`)
		require.Contains(t, response.Body.String(), `"mode":"usage_context"`)
		require.Contains(t, response.Body.String(), fmt.Sprintf(`"upTo":%d`, step.bound))

		stored := client.ChannelModelPrice.Query().OnlyX(ctx)
		pricing := stored.Price.Items[0].Pricing
		require.Equal(t, objects.PricingModeContext, pricing.Mode)
		require.Equal(t, step.bound, *pricing.UsageTiered.Tiers[0].UpTo)
		require.Equal(t, fmt.Sprint(step.rate), pricing.UsageTiered.Tiers[1].PricePerUnit.String())
		require.Equal(t, step.versions, client.ChannelModelPriceVersion.Query().CountX(ctx))
	}
}

func TestVariantOnlyPriceGraphQLRoundTrip(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	ch := client.Channel.Create().SetType(channel.TypeOpenai).SetName("Variant-only price").
		SetBaseURL("https://example.invalid").SetCredentials(objects.ChannelCredentials{APIKey: "test"}).
		SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SaveX(ctx)
	server := handler.NewDefaultServer(NewExecutableSchema(Config{
		Resolvers: &Resolver{channelService: biz.NewChannelServiceForTest(client)},
	}))
	payload, err := json.Marshal(map[string]any{
		"query": `mutation Save($channelId: ID!, $input: [SaveChannelModelPriceInput!]!) {
			saveChannelModelPrices(channelId: $channelId, input: $input) {
				price { items { pricing { mode } promptWriteCacheVariants { variantCode pricing { mode usagePerUnit } } } }
			}
		}`,
		"variables": map[string]any{
			"channelId": fmt.Sprintf("gid://axonhub/Channel/%d", ch.ID),
			"input": []any{map[string]any{
				"modelId": "model", "price": map[string]any{
					"items": []any{map[string]any{
						"itemCode": "prompt_write_cached_tokens", "pricing": map[string]any{"mode": nil},
						"promptWriteCacheVariants": []any{map[string]any{
							"variantCode": "five_min", "pricing": map[string]any{"mode": "usage_per_unit", "usagePerUnit": "2.5"},
						}},
					}},
				},
			}},
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(payload)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), `"errors"`)
	require.Contains(t, response.Body.String(), `"mode":null`)
	stored := client.ChannelModelPrice.Query().OnlyX(ctx)
	require.Empty(t, stored.Price.Items[0].Pricing.Mode)
	require.Equal(t, "2.5", stored.Price.Items[0].PromptWriteCacheVariants[0].Pricing.UsagePerUnit.String())

	var invalid map[string]any
	require.NoError(t, json.Unmarshal(payload, &invalid))
	item := invalid["variables"].(map[string]any)["input"].([]any)[0].(map[string]any)["price"].(map[string]any)["items"].([]any)[0].(map[string]any)
	delete(item, "promptWriteCacheVariants")
	invalidPayload, err := json.Marshal(invalid)
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(invalidPayload)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, req)
	require.Contains(t, response.Body.String(), `"errors"`, "missing mode remains invalid without cache-write variants")
	require.Equal(t, 1, client.ChannelModelPriceVersion.Query().CountX(ctx))
}

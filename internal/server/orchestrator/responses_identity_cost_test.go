package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	responsestransformer "github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestResponsesIdentityPreservesInjectedCost(t *testing.T) {
	for _, transport := range []struct {
		name   string
		stream bool
		target llm.APIFormat
	}{
		{"json", false, llm.APIFormatOpenAIResponse},
		{"sse", true, llm.APIFormatOpenAIResponse},
		{"adapted-json", false, llm.APIFormatOpenAIResponseWebSocket},
		{"adapted-sse", true, llm.APIFormatOpenAIResponseWebSocket},
	} {
		for _, tt := range []struct {
			name, requested, applied                string
			disabled, passThrough, noPrice, noUsage bool
			wantCost                                *float64
		}{
			{name: "standard", wantCost: lo.ToPtr(0.000005)},
			{name: "ultrafast", requested: "ultrafast", applied: "default", wantCost: lo.ToPtr(0.00003)},
			{name: "provider-priority", requested: "ultrafast", applied: "priority", wantCost: lo.ToPtr(0.00001)},
			{name: "provider-flex", requested: "ultrafast", applied: "flex", wantCost: lo.ToPtr(0.000005)},
			{name: "disabled", requested: "ultrafast", disabled: true, wantCost: lo.ToPtr(9.99)},
			{name: "explicit-pass-through", requested: "ultrafast", passThrough: true, wantCost: lo.ToPtr(9.99)},
			{name: "missing-price", noPrice: true},
			{name: "missing-usage", noUsage: true},
		} {
			if tt.passThrough && transport.target != llm.APIFormatOpenAIResponse {
				continue // Explicit pass-through only applies to identical formats.
			}
			t.Run(transport.name+"/"+tt.name, func(t *testing.T) {
				middleware, ctx, channelID := setupUsageCostMiddleware(t, llm.APIFormatOpenAIResponse.String(), "gpt-6-astra")
				state := middleware.inbound.state
				channel := state.UsageLogService.ChannelService.GetEnabledChannel(channelID)
				channel.Settings = &objects.ChannelSettings{PassThroughBody: lo.ToPtr(tt.passThrough)}
				state.CurrentCandidate = &ChannelModelsCandidate{Channel: channel}
				state.LlmRequest = &llm.Request{APIFormat: llm.APIFormatOpenAIResponse, Model: "client-alias"}
				state.RawProviderRequest = &httpclient.Request{APIFormat: transport.target.String()}
				state.RequestedServiceTier = tt.requested
				state.RequestPricingOverride = tt.requested
				state.RequestPricingOverridePolicy = biz.RequestPricingOverrideWhenAppliedDefault
				if tt.noPrice {
					state.RequestExec.ModelID = "unpriced-model"
				}
				require.NoError(t, state.SystemService.SetInjectUsageCostEnabled(ctx, !tt.disabled))
				outbound := &PersistentOutboundTransformer{state: state}
				require.Equal(t, tt.passThrough, outbound.isPassThroughEnabled(ctx, state.SystemService))

				usage := &llm.Usage{PromptTokens: 100, CompletionTokens: 200, TotalTokens: 300, Cost: lo.ToPtr(9.99)}
				if tt.noUsage {
					usage = nil
				}
				llmResponse, err := middleware.OnOutboundLlmResponse(ctx, &llm.Response{
					ID: "provider-response", Model: "gpt-6-astra", ServiceTier: tt.applied, Usage: usage,
				})
				require.NoError(t, err)
				transformed, err := responsestransformer.NewInboundTransformer().TransformResponse(ctx, llmResponse)
				require.NoError(t, err)

				raw := []byte(`{"id":"provider-response","model":"gpt-6-astra","status":"completed","vendor_unknown":9007199254740993,"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"cost":9.99,"extra":{"keep":true}}}`)
				raw, err = sjson.SetBytes(raw, "service_tier", tt.applied)
				require.NoError(t, err)
				if tt.noUsage {
					raw, err = sjson.DeleteBytes(raw, "usage")
					require.NoError(t, err)
				}
				var wire []byte
				prefix := ""
				if transport.stream {
					terminal := func(body []byte) *httpclient.StreamEvent {
						data, err := json.Marshal(map[string]any{"type": "response.completed", "response": json.RawMessage(body)})
						require.NoError(t, err)
						return &httpclient.StreamEvent{Type: "response.completed", Data: data}
					}
					state.RawStreamCh = make(chan *httpclient.StreamEvent, 1)
					state.RawStreamCh <- terminal(raw)
					close(state.RawStreamCh)
					result, err := applyPassThroughStream(outbound, state.SystemService).OnInboundRawStream(ctx, testHTTPStream([]*httpclient.StreamEvent{terminal(transformed.Body)}))
					require.NoError(t, err)
					t.Cleanup(func() { _ = result.Close() })
					require.True(t, result.Next())
					wire = result.Current().Data
					require.Equal(t, wire, result.Current().Data)
					require.False(t, result.Next())
					require.NoError(t, result.Err())
					prefix = "response."
				} else {
					state.RawProviderResponse = &httpclient.Response{Body: raw}
					result, err := applyPassThroughResponse(outbound, state.SystemService).OnInboundRawResponse(ctx, transformed)
					require.NoError(t, err)
					wire = result.Body
				}
				require.Equal(t, "9007199254740993", gjson.GetBytes(wire, prefix+"vendor_unknown").Raw)
				cost := gjson.GetBytes(wire, prefix+"usage.cost")
				if tt.wantCost == nil {
					require.False(t, cost.Exists(), "unexpected cost: %s", wire)
				} else {
					require.True(t, cost.Exists(), "injected cost was lost from wire payload: %s", wire)
					require.InDelta(t, *tt.wantCost, cost.Float(), 1e-12)
				}
				if tt.noUsage {
					require.False(t, gjson.GetBytes(wire, prefix+"usage").Exists())
				} else {
					require.True(t, gjson.GetBytes(wire, prefix+"usage.extra.keep").Bool())
				}
			})
		}
	}
}

func TestResponsesCostInjectorKeepsIndependentTierHistory(t *testing.T) {
	middleware, ctx, channelID := setupUsageCostMiddleware(t, llm.APIFormatOpenAIResponse.String(), "gpt-6-astra")
	state := middleware.inbound.state
	channel := state.UsageLogService.ChannelService.GetEnabledChannel(channelID)
	channel.Settings = &objects.ChannelSettings{PassThroughBody: lo.ToPtr(false)}
	state.CurrentCandidate = &ChannelModelsCandidate{Channel: channel}
	state.LlmRequest = &llm.Request{APIFormat: llm.APIFormatOpenAIResponse}
	state.RawProviderRequest = &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String()}
	state.RequestedServiceTier = "ultrafast"
	state.RequestPricingOverride = "ultrafast"
	state.RequestPricingOverridePolicy = biz.RequestPricingOverrideWhenAppliedDefault
	inject := newResponsesCostInjector(ctx, &PersistentOutboundTransformer{state: state}, state.SystemService)
	require.NotNil(t, inject)
	created := []byte(`{"type":"response.created","response":{"id":"resp-1","service_tier":"priority"}}`)
	require.Equal(t, created, inject(created))
	state.AppliedServiceTier = "ultrafast"
	state.RequestedServiceTier = "default"
	state.RequestExec.ModelID = "unpriced-model"
	completed := inject([]byte(`{"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300}}}`))
	require.InDelta(t, 0.00001, gjson.GetBytes(completed, "response.usage.cost").Float(), 1e-12)
}

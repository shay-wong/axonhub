package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestServiceTierFromHTTPRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *httpclient.Request
		want string
	}{
		{
			name: "prefers final orchestrator body",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":"priority"}`),
				JSONBody:  []byte(`{"service_tier":"default"}`),
			},
			want: "priority",
		},
		{
			name: "falls back to JSON representation",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				JSONBody:  []byte(`{"service_tier":"flex"}`),
			},
			want: "flex",
		},
		{
			name: "does not fall back when valid wire body tier is not a string",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":1}`),
				JSONBody:  []byte(`{"service_tier":"priority"}`),
			},
			want: "",
		},
		{
			name: "does not fall back when valid wire body omits tier",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"model":"gpt-5"}`),
				JSONBody:  []byte(`{"service_tier":"priority"}`),
			},
			want: "",
		},
		{
			name: "falls back when wire body is not JSON",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte("multipart body"),
				JSONBody:  []byte(`{"service_tier":"priority"}`),
			},
			want: "priority",
		},
		{
			name: "ignores service tier outside OpenAI formats",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatAnthropicMessage),
				Body:      []byte(`{"service_tier":"priority"}`),
			},
			want: "",
		},
		{
			name: "handles missing request",
			req:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, serviceTierFromHTTPRequest(tt.req))
		})
	}
}

func TestRequestPricingOverrideFromHTTPRequest(t *testing.T) {
	tests := []struct {
		name        string
		req         *httpclient.Request
		channelType channel.Type
		want        requestPricingOverride
	}{
		{
			name: "uses Codex priority request because response tier is not authoritative",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":"priority"}`),
			},
			channelType: channel.TypeCodex,
			want: requestPricingOverride{
				ServiceTier: llm.ServiceTierPriority,
				Policy:      biz.RequestPricingOverrideWhenAppliedDefault,
			},
		},
		{
			name: "uses Codex ultrafast request because response tier is not authoritative",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":"ultrafast"}`),
			},
			channelType: channel.TypeCodex,
			want: requestPricingOverride{
				ServiceTier: llm.ServiceTierUltrafast,
				Policy:      biz.RequestPricingOverrideWhenAppliedDefault,
			},
		},
		{
			name: "does not override public OpenAI applied tier",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":"priority"}`),
			},
			channelType: channel.TypeOpenaiResponses,
			want:        requestPricingOverride{},
		},
		{
			name: "maps Anthropic fast speed to priority pricing",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatAnthropicMessage),
				Body:      []byte(`{"speed":"fast"}`),
			},
			channelType: channel.TypeAnthropic,
			want: requestPricingOverride{
				ServiceTier: llm.ServiceTierPriority,
				Policy:      biz.RequestPricingOverrideAlways,
			},
		},
		{
			name: "ignores speed field outside Anthropic format",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"speed":"fast"}`),
			},
			channelType: channel.TypeOpenaiResponses,
			want:        requestPricingOverride{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, requestPricingOverrideFromHTTPRequest(tt.req, tt.channelType))
		})
	}
}

func TestSpeedModeFromHTTPRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *httpclient.Request
		want string
	}{
		{
			name: "maps OpenAI priority service tier to fast",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":"priority"}`),
			},
			want: "fast",
		},
		{
			name: "uses Anthropic fast speed",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatAnthropicMessage),
				Body:      []byte(`{"speed":"fast"}`),
			},
			want: "fast",
		},
		{
			name: "maps OpenAI ultrafast service tier",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":"ultrafast"}`),
			},
			want: "ultrafast",
		},
		{
			name: "does not mix flex service tier into speed mode",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"service_tier":"flex"}`),
			},
			want: "",
		},
		{
			name: "ignores fast speed outside Anthropic format",
			req: &httpclient.Request{
				APIFormat: string(llm.APIFormatOpenAIResponse),
				Body:      []byte(`{"speed":"fast"}`),
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, speedModeFromHTTPRequest(tt.req))
		})
	}
}

func TestCaptureRequestedServiceTier_ResetsServiceTierForEveryAttempt(t *testing.T) {
	state := stateWithChannelType(channel.TypeCodex)
	middleware := captureRequestedServiceTier(&PersistentOutboundTransformer{state: state})
	state.RequestedServiceTier = "stale"
	state.AppliedServiceTier = "stale"
	state.RequestPricingOverride = "stale"
	state.RequestPricingOverridePolicy = biz.RequestPricingOverrideAlways
	state.SpeedMode = "stale"
	state.UsageLogEligible = true

	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"service_tier":"priority"}`),
	})
	require.NoError(t, err)
	require.Equal(t, llm.ServiceTierPriority, state.RequestedServiceTier)
	require.Equal(t, llm.ServiceTierPriority, state.RequestPricingOverride)
	require.Equal(t, biz.RequestPricingOverrideWhenAppliedDefault, state.RequestPricingOverridePolicy)
	require.Equal(t, "fast", state.SpeedMode)
	require.Empty(t, state.AppliedServiceTier)
	require.False(t, state.UsageLogEligible)

	state.RequestedServiceTier = "stale"
	state.RequestPricingOverride = "stale"
	state.RequestPricingOverridePolicy = biz.RequestPricingOverrideAlways
	state.SpeedMode = "stale"

	_, err = middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"model":"gpt-5"}`),
	})
	require.NoError(t, err)
	require.Empty(t, state.RequestedServiceTier)
	require.Empty(t, state.RequestPricingOverride)
	require.Equal(t, biz.RequestPricingOverrideDisabled, state.RequestPricingOverridePolicy)
	require.Empty(t, state.SpeedMode)
}

func TestCaptureRequestedServiceTier_PublicOpenAIFastKeepsResponseAuthoritative(t *testing.T) {
	state := stateWithChannelType(channel.TypeOpenaiResponses)
	middleware := captureRequestedServiceTier(&PersistentOutboundTransformer{state: state})

	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"service_tier":"priority"}`),
	})
	require.NoError(t, err)
	require.Equal(t, llm.ServiceTierPriority, state.RequestedServiceTier)
	require.Empty(t, state.RequestPricingOverride)
	require.Equal(t, biz.RequestPricingOverrideDisabled, state.RequestPricingOverridePolicy)
	require.Equal(t, "fast", state.SpeedMode)
}

func TestCaptureRequestedServiceTier_AnthropicFastKeepsProtocolTierEmpty(t *testing.T) {
	state := stateWithChannelType(channel.TypeAnthropic)
	middleware := captureRequestedServiceTier(&PersistentOutboundTransformer{state: state})

	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{
		APIFormat: string(llm.APIFormatAnthropicMessage),
		Body:      []byte(`{"speed":"fast"}`),
	})
	require.NoError(t, err)
	require.Empty(t, state.RequestedServiceTier)
	require.Equal(t, llm.ServiceTierPriority, state.RequestPricingOverride)
	require.Equal(t, biz.RequestPricingOverrideAlways, state.RequestPricingOverridePolicy)
	require.Equal(t, "fast", state.SpeedMode)
}

func TestCaptureRequestedServiceTier_AfterRequestMutation(t *testing.T) {
	state := &PersistenceState{}
	outbound := &PersistentOutboundTransformer{state: state}
	persist := persistRequestExecution(outbound)
	capture := captureRequestedServiceTier(outbound)
	request := &httpclient.Request{
		APIFormat: string(llm.APIFormatOpenAIResponse),
		Body:      []byte(`{"service_tier":"default"}`),
	}

	request, err := persist.OnOutboundRawRequest(t.Context(), request)
	require.NoError(t, err)
	request.Body = []byte(`{"service_tier":"priority"}`)
	state.CurrentCandidate = &ChannelModelsCandidate{
		Channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeCodex}},
	}
	request, err = capture.OnOutboundRawRequest(t.Context(), request)
	require.NoError(t, err)

	require.Equal(t, "priority", state.RequestedServiceTier)
	require.Equal(t, "priority", state.RequestPricingOverride)
	require.Equal(t, biz.RequestPricingOverrideWhenAppliedDefault, state.RequestPricingOverridePolicy)
	require.Equal(t, "fast", state.SpeedMode)
	require.JSONEq(t, `{"service_tier":"priority"}`, string(request.Body))
}

func stateWithChannelType(channelType channel.Type) *PersistenceState {
	return &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: &biz.Channel{Channel: &ent.Channel{Type: channelType}},
		},
	}
}

package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"

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
				Body:     []byte(`{"service_tier":"priority"}`),
				JSONBody: []byte(`{"service_tier":"default"}`),
			},
			want: "priority",
		},
		{
			name: "falls back to JSON representation",
			req:  &httpclient.Request{JSONBody: []byte(`{"service_tier":"flex"}`)},
			want: "flex",
		},
		{
			name: "does not fall back when valid wire body tier is not a string",
			req: &httpclient.Request{
				Body:     []byte(`{"service_tier":1}`),
				JSONBody: []byte(`{"service_tier":"priority"}`),
			},
			want: "",
		},
		{
			name: "does not fall back when valid wire body omits tier",
			req: &httpclient.Request{
				Body:     []byte(`{"model":"gpt-5"}`),
				JSONBody: []byte(`{"service_tier":"priority"}`),
			},
			want: "",
		},
		{
			name: "falls back when wire body is not JSON",
			req: &httpclient.Request{
				Body:     []byte("multipart body"),
				JSONBody: []byte(`{"service_tier":"priority"}`),
			},
			want: "priority",
		},
		{
			name: "ignores non-string tier",
			req:  &httpclient.Request{Body: []byte(`{"service_tier":1}`)},
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

func TestCaptureRequestedServiceTier_ResetsServiceTierForEveryAttempt(t *testing.T) {
	state := &PersistenceState{}
	middleware := captureRequestedServiceTier(&PersistentOutboundTransformer{state: state})
	state.RequestedServiceTier = "stale"
	state.AppliedServiceTier = "stale"
	state.UsageLogEligible = true

	_, err := middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{
		Body: []byte(`{"service_tier":"priority"}`),
	})
	require.NoError(t, err)
	require.Equal(t, llm.ServiceTierPriority, state.RequestedServiceTier)
	require.Empty(t, state.AppliedServiceTier)
	require.False(t, state.UsageLogEligible)

	state.RequestedServiceTier = "stale"

	_, err = middleware.OnOutboundRawRequest(t.Context(), &httpclient.Request{
		Body: []byte(`{"model":"gpt-5"}`),
	})
	require.NoError(t, err)
	require.Empty(t, state.RequestedServiceTier)
}

func TestCaptureRequestedServiceTier_AfterRequestMutation(t *testing.T) {
	state := &PersistenceState{}
	outbound := &PersistentOutboundTransformer{state: state}
	persist := persistRequestExecution(outbound)
	capture := captureRequestedServiceTier(outbound)
	request := &httpclient.Request{Body: []byte(`{"service_tier":"default"}`)}

	request, err := persist.OnOutboundRawRequest(t.Context(), request)
	require.NoError(t, err)
	request.Body = []byte(`{"service_tier":"priority"}`)
	request, err = capture.OnOutboundRawRequest(t.Context(), request)
	require.NoError(t, err)

	require.Equal(t, "priority", state.RequestedServiceTier)
	require.JSONEq(t, `{"service_tier":"priority"}`, string(request.Body))
}

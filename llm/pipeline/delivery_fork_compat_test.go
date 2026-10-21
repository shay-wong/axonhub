package pipeline_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

type deliveryProbeStream struct {
	streams.Stream[*httpclient.StreamEvent]
	acknowledged  bool
	waited        bool
	boundaryCalls int
}

func (s *deliveryProbeStream) AcknowledgeDelivery()       { s.acknowledged = true }
func (s *deliveryProbeStream) DeliveryAcknowledged() bool { return s.acknowledged }
func (s *deliveryProbeStream) WaitForFinalization()       { s.waited = true }
func (s *deliveryProbeStream) ExpectedStreamChoices() int { return 2 }
func (s *deliveryProbeStream) Deliver(_ context.Context, write func() (bool, error)) error {
	s.boundaryCalls++
	terminal, err := write()
	if err == nil && terminal {
		s.AcknowledgeDelivery()
	}
	return err
}

func TestPipelineFinalErrorWrapperPreservesDeliveryCapabilities(t *testing.T) {
	outbound, err := openai.NewOutboundTransformer("https://example.test", "test-key")
	require.NoError(t, err)
	executor := &mockExecutor{doStreamFunc: func(context.Context, *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
		return streams.SliceStream([]*httpclient.StreamEvent{{Data: []byte(`{"id":"chat","model":"m","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)}}), nil
	}}
	var probe *deliveryProbeStream
	p := pipeline.NewFactory(executor).Pipeline(openai.NewInboundTransformer(), outbound,
		pipeline.WithMiddlewares(pipeline.OnInboundRawStream("delivery-probe", func(_ context.Context, source streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
			probe = &deliveryProbeStream{Stream: source}
			return probe, nil
		})))
	result, err := p.Process(t.Context(), &httpclient.Request{Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, result.EventStream.Close()) })
	boundary, ok := result.EventStream.(streams.DeliveryBoundary)
	require.True(t, ok)
	writeErr := errors.New("downstream write failed")
	require.ErrorIs(t, boundary.Deliver(t.Context(), func() (bool, error) { return true, writeErr }), writeErr)
	require.False(t, probe.acknowledged)
	require.NoError(t, boundary.Deliver(t.Context(), func() (bool, error) { return true, nil }))
	require.Equal(t, 2, probe.boundaryCalls)
	require.True(t, result.EventStream.(streams.DeliveryAcknowledger).DeliveryAcknowledged())
	result.EventStream.(streams.FinalizationWaiter).WaitForFinalization()
	require.True(t, probe.waited)
	require.Equal(t, 2, result.EventStream.(interface{ ExpectedStreamChoices() int }).ExpectedStreamChoices())
}

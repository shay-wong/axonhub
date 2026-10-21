package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	responsestransformer "github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestApplyPassThroughStream_LifecycleDefersCloseUntilDelivery(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		name := "write-failed"
		if delivered {
			name = "delivered"
		}
		t.Run(name, func(t *testing.T) {
			middleware, ctx, channelID := setupUsageCostMiddleware(t, llm.APIFormatOpenAIResponse.String(), "gpt-6-astra")
			owner, execution := newStreamLifecycle(ctx)
			t.Cleanup(owner.Close)
			state := middleware.inbound.state
			channel := state.UsageLogService.ChannelService.GetEnabledChannel(channelID)
			channel.Settings = &objects.ChannelSettings{PassThroughBody: lo.ToPtr(false)}
			state.CurrentCandidate = &ChannelModelsCandidate{Channel: channel}
			state.LlmRequest = &llm.Request{
				APIFormat: llm.APIFormatOpenAIResponse,
				Model:     "mapped-model",
				RawRequest: &httpclient.Request{
					Body: []byte(`{"model":"client-alias"}`),
				},
			}
			state.RawProviderRequest = &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String()}
			state.RequestedServiceTier = "ultrafast"
			state.RequestPricingOverride = "ultrafast"
			state.RequestPricingOverridePolicy = biz.RequestPricingOverrideWhenAppliedDefault
			require.NoError(t, state.SystemService.SetInjectUsageCostEnabled(ctx, true))
			raw := &httpclient.StreamEvent{Data: []byte(`{"type":"response.completed","response":{"id":"original-id","model":"mapped-model","status":"completed","service_tier":"default","output":[],"vendor_unknown":9007199254740993,"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300,"extra":{"keep":true}}}}`)}
			state.RawStreamCh = make(chan *httpclient.StreamEvent, 1)
			state.RawStreamCh <- raw
			close(state.RawStreamCh)
			converted := &httpclient.StreamEvent{Data: []byte(`{"type":"response.completed","response":{"id":"converted-id","status":"completed","output":[]}}`)}
			persistent := NewInboundPersistentStream(execution, testHTTPStream([]*httpclient.StreamEvent{converted}), nil, nil, nil, responsestransformer.NewInboundTransformer(), nil, state)
			result, err := applyPassThroughStream(&PersistentOutboundTransformer{state: state}, state.SystemService).OnInboundRawStream(execution, persistent)
			require.NoError(t, err)
			t.Cleanup(func() { _ = result.Close() })

			require.True(t, result.Next())
			wire := result.Current().Data
			require.NoError(t, execution.Err(), "validation must not close the lifecycle before terminal delivery")
			require.False(t, owner.DeliveryAcknowledged())
			require.Equal(t, "original-id", gjson.GetBytes(wire, "response.id").String())
			require.Equal(t, "client-alias", gjson.GetBytes(wire, "response.model").String())
			require.Equal(t, "9007199254740993", gjson.GetBytes(wire, "response.vendor_unknown").Raw)
			require.True(t, gjson.GetBytes(wire, "response.usage.extra.keep").Bool())
			require.InDelta(t, 0.00003, gjson.GetBytes(wire, "response.usage.cost").Float(), 1e-12)
			writeErr := errors.New("downstream write failed")
			err = result.(streams.DeliveryBoundary).Deliver(ctx, func() (bool, error) {
				if delivered {
					return true, nil
				}
				return false, writeErr
			})
			if delivered {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, writeErr)
			}
			require.NoError(t, result.Close())
			require.Equal(t, delivered, owner.DeliveryAcknowledged())
			require.Equal(t, delivered, state.StreamCompleted)
		})
	}
}

func TestResponsesIdentityStream_LifecycleStopsBackpressuredSource(t *testing.T) {
	for _, adapterFailure := range []bool{false, true} {
		name := "explicit-close"
		if adapterFailure {
			name = "adapter-failure"
		}
		t.Run(name, func(t *testing.T) {
			downstream, cancel := context.WithCancel(t.Context())
			defer cancel()
			owner, ctx := newStreamLifecycle(downstream)
			t.Cleanup(owner.Close)
			state := &PersistenceState{
				CurrentCandidate:   &ChannelModelsCandidate{Channel: &biz.Channel{Channel: &ent.Channel{Name: "responses"}}},
				LlmRequest:         &llm.Request{APIFormat: llm.APIFormatOpenAIResponseWebSocket},
				RawProviderRequest: &httpclient.Request{APIFormat: llm.APIFormatOpenAIResponse.String()},
			}
			outbound := &PersistentOutboundTransformer{state: state}
			release := make(chan struct{})
			events := make([]*httpclient.StreamEvent, 130)
			for i := range events {
				events[i] = &httpclient.StreamEvent{Data: []byte(`{"type":"response.output_text.delta","delta":"x"}`)}
			}
			if adapterFailure {
				events[0] = &httpclient.StreamEvent{Data: []byte(`not-json`)}
			}
			captured, err := captureRawProviderStream(outbound, nil).OnOutboundRawStream(ctx, &gatedStream{events: events, gateAt: 0, release: release})
			require.NoError(t, err)
			result, err := applyPassThroughStream(outbound, nil).OnInboundRawStream(ctx, captured)
			require.NoError(t, err)
			close(release)
			require.Eventually(t, func() bool { return len(state.RawStreamCh) == cap(state.RawStreamCh) }, time.Second, time.Millisecond)
			finished := make(chan error, 1)
			go func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						finished <- errors.New("pass-through test consumer panicked")
					}
				}()
				if adapterFailure {
					if result.Next() {
						finished <- errors.New("invalid adapter event was published")
						return
					}
					finished <- result.Err()
					return
				}
				// Exercise identity Close directly: raw Close only detaches the consumer.
				finished <- result.(*passThroughDeliveryStream).Stream.Close()
			}()
			select {
			case err := <-finished:
				if adapterFailure {
					require.ErrorIs(t, err, transformer.ErrInvalidResponse)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(time.Second):
				cancel()
				<-finished
				t.Fatal("identity stream waited for a backpressured producer without stopping it")
			}
			require.NoError(t, result.Close())
		})
	}
}

package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
)

// mockOutboundWithRetryLimit extends mockOutbound with the optional
// ChannelSameChannelRetryLimit interface so tests can exercise the channel-level
// override of max same-channel retries.
type mockOutboundWithRetryLimit struct {
	mockOutbound

	sameChannelLimit func() (int, bool)
}

func (m *mockOutboundWithRetryLimit) SameChannelRetryLimit() (int, bool) {
	if m.sameChannelLimit != nil {
		return m.sameChannelLimit()
	}

	return 0, false
}

type mockContextOutboundWithRetryLimit struct {
	*mockOutboundWithRetryLimit
	failures int
}

func (m *mockContextOutboundWithRetryLimit) CanRetryContext(context.Context, error) bool {
	m.failures++
	return true
}

func TestPipeline_OptionalKeyRetryBudget(t *testing.T) {
	for _, tt := range []struct {
		name          string
		global, limit int
		override      bool
		keyFloor      int
		disabled      bool
		wantRetries   int
	}{
		{name: "default policy keeps global count", global: 3, wantRetries: 3},
		{name: "zero override without key retry", global: 3, override: true},
		{name: "zero override with key retry", global: 3, override: true, keyFloor: 2, wantRetries: 2},
		{name: "override lower than key floor", global: 3, limit: 1, override: true, keyFloor: 2, wantRetries: 2},
		{name: "override higher than key floor", global: 3, limit: 4, override: true, keyFloor: 2, wantRetries: 4},
		{name: "no override with key retry", keyFloor: 2, wantRetries: 2},
		{name: "disabled ignores override and keys", global: 3, limit: 4, override: true, keyFloor: 2, disabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			outbound := &mockContextOutboundWithRetryLimit{mockOutboundWithRetryLimit: &mockOutboundWithRetryLimit{
				mockOutbound:     mockOutbound{maxSameChannelRetries: func() int { return tt.keyFloor }},
				sameChannelLimit: func() (int, bool) { return tt.limit, tt.override },
			}}
			p := &pipeline{
				Executor: &mockExecutor{do: func(context.Context, *httpclient.Request) (*httpclient.Response, error) {
					attempts++
					return nil, errors.New("temporary failure")
				}},
				Inbound: &mockInbound{}, Outbound: outbound,
				maxSameChannelRetries: tt.global, retriesDisabled: tt.disabled,
			}
			require.Equal(t, tt.wantRetries > 0, p.hasStreamRetryBudget())
			_, err := p.Process(t.Context(), &httpclient.Request{})
			require.Error(t, err)
			require.Equal(t, tt.wantRetries+1, attempts)
			require.Equal(t, attempts, outbound.failures, "register the failed key even after the retry budget is exhausted")
		})
	}
}

func TestPipeline_MaxSameChannelRetriesFor(t *testing.T) {
	t.Run("falls back to the global value when the outbound has no override", func(t *testing.T) {
		p := &pipeline{Outbound: &mockOutbound{}, maxSameChannelRetries: 2}
		require.Equal(t, 2, p.maxSameChannelRetriesFor())
	})

	t.Run("falls back to the global value when the outbound does not implement the interface", func(t *testing.T) {
		p := &pipeline{Outbound: &mockOutbound{}, maxSameChannelRetries: 5}
		require.Equal(t, 5, p.maxSameChannelRetriesFor())
	})

	t.Run("uses the channel-level override when provided", func(t *testing.T) {
		outbound := &mockOutboundWithRetryLimit{
			sameChannelLimit: func() (int, bool) { return 7, true },
		}
		p := &pipeline{Outbound: outbound, maxSameChannelRetries: 2}
		require.Equal(t, 7, p.maxSameChannelRetriesFor())
	})

	t.Run("falls back to the global value when the override declines", func(t *testing.T) {
		outbound := &mockOutboundWithRetryLimit{
			sameChannelLimit: func() (int, bool) { return 0, false },
		}
		p := &pipeline{Outbound: outbound, maxSameChannelRetries: 2}
		require.Equal(t, 2, p.maxSameChannelRetriesFor())
	})

	t.Run("a disabled retry policy ignores the channel override", func(t *testing.T) {
		outbound := &mockOutboundWithRetryLimit{
			sameChannelLimit: func() (int, bool) { return 7, true },
		}
		p := &pipeline{Outbound: outbound, maxSameChannelRetries: 0, retriesDisabled: true}
		require.Equal(t, 0, p.maxSameChannelRetriesFor(), "the override only adjusts the count, it cannot switch retries back on")
	})

	t.Run("zero override preserves the outbound key-rotation floor", func(t *testing.T) {
		outbound := &mockOutboundWithRetryLimit{
			mockOutbound:     mockOutbound{maxSameChannelRetries: func() int { return 2 }},
			sameChannelLimit: func() (int, bool) { return 0, true },
		}
		p := &pipeline{Outbound: outbound, maxSameChannelRetries: 0}
		require.Equal(t, 2, p.maxSameChannelRetriesFor())
	})
}

// TestPipeline_Process_ChannelLevelSameChannelRetries pins that a channel-level
// override actually changes how many times Process retries the same channel.
func TestPipeline_Process_ChannelLevelSameChannelRetries(t *testing.T) {
	ctx := context.Background()

	t.Run("zero override stops retries when key rotation is disabled", func(t *testing.T) {
		execCalls := 0
		executor := &mockExecutor{
			do: func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
				execCalls++
				return nil, errors.New("temporary error")
			},
		}

		outbound := &mockOutboundWithRetryLimit{
			mockOutbound: mockOutbound{
				canRetry:        func(err error) bool { return true },
				hasMoreChannels: func() bool { return false },
			},
			sameChannelLimit: func() (int, bool) { return 0, true },
		}

		p := &pipeline{
			Executor:              executor,
			Inbound:               &mockInbound{},
			Outbound:              outbound,
			maxSameChannelRetries: 3,
		}

		_, err := p.Process(ctx, &httpclient.Request{})
		require.Error(t, err)
		require.Equal(t, 1, execCalls, "a zero override must stop same-channel retries")
	})

	t.Run("override raises the retry count above the global value", func(t *testing.T) {
		execCalls := 0
		executor := &mockExecutor{
			do: func(ctx context.Context, req *httpclient.Request) (*httpclient.Response, error) {
				execCalls++
				if execCalls > 3 {
					return &httpclient.Response{}, nil
				}

				return nil, errors.New("temporary error")
			},
		}

		outbound := &mockOutboundWithRetryLimit{
			mockOutbound: mockOutbound{
				canRetry:        func(err error) bool { return true },
				hasMoreChannels: func() bool { return false },
			},
			sameChannelLimit: func() (int, bool) { return 3, true },
		}

		p := &pipeline{
			Executor:              executor,
			Inbound:               &mockInbound{},
			Outbound:              outbound,
			maxSameChannelRetries: 1,
		}

		res, err := p.Process(ctx, &httpclient.Request{})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.Equal(t, 4, execCalls, "the channel override (3) must drive the retry count, not the global value (1)")
	})
}

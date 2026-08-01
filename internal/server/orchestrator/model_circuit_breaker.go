package orchestrator

import (
	"context"
	"errors"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

func withModelCircuitBreaker(outbound *PersistentOutboundTransformer, modelCircuitBreaker *biz.ModelCircuitBreaker) pipeline.Middleware {
	return &modelCircuitBreakerTracker{
		outbound:            outbound,
		modelCircuitBreaker: modelCircuitBreaker,
	}
}

type modelCircuitBreakerTracker struct {
	pipeline.DummyMiddleware

	outbound            *PersistentOutboundTransformer
	modelCircuitBreaker *biz.ModelCircuitBreaker

	probeActive    bool
	probeChannelID int
	probeModelID   string
}

func (m *modelCircuitBreakerTracker) Name() string {
	return "model-circuit-breaker-tracker"
}

func (m *modelCircuitBreakerTracker) OnOutboundRawRequest(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	if m.outbound == nil || m.outbound.state == nil ||
		m.outbound.state.RoutingPolicy.LoadBalancerStrategy != biz.LoadBalancerStrategyCircuitBreaker ||
		m.modelCircuitBreaker == nil {
		return request, nil
	}
	if shouldSkipHealthStateTrackingForState(ctx, m.outbound.state) {
		return request, nil
	}

	channel := m.outbound.GetCurrentChannel()
	modelID := m.outbound.GetRequestedModel()
	if channel == nil || modelID == "" {
		return request, nil
	}

	stats := m.modelCircuitBreaker.GetModelCircuitBreakerStats(ctx, channel.ID, modelID)
	if stats == nil || stats.State != biz.StateOpen {
		return request, nil
	}

	if !m.modelCircuitBreaker.TryBeginProbe(ctx, channel.ID, modelID) {
		log.Debug(ctx, "skipping candidate by circuit breaker: probe conditions not met or another probe in progress",
			log.Int("channel_id", channel.ID),
			log.String("model_id", modelID),
		)

		return nil, errSkipCandidateByCircuitBreaker
	}

	m.probeActive = true
	m.probeChannelID = channel.ID
	m.probeModelID = modelID

	return request, nil
}

func (m *modelCircuitBreakerTracker) OnOutboundLlmResponse(ctx context.Context, response *llm.Response) (*llm.Response, error) {
	if !m.shouldTrack() {
		return response, nil
	}
	if shouldSkipHealthStateTrackingForState(ctx, m.outbound.state) {
		return response, nil
	}

	channel := m.outbound.GetCurrentChannel()
	modelID := m.outbound.GetRequestedModel()
	if channel == nil || modelID == "" {
		m.releaseProbeLease()
		return response, nil
	}
	wasProbe := m.probeActive
	m.releaseProbeLease()

	switch response.TerminalOutcome() {
	case llm.ResponseTerminalOutcomeFailed, llm.ResponseTerminalOutcomeIncomplete:
		m.modelCircuitBreaker.RecordError(ctx, channel.ID, modelID, wasProbe)
	case llm.ResponseTerminalOutcomeCanceled:
		m.outbound.state.ModelCircuitBreakerRecorded = true
		return response, nil
	default:
		m.modelCircuitBreaker.RecordSuccess(ctx, channel.ID, modelID)
	}
	m.outbound.state.ModelCircuitBreakerRecorded = true

	return response, nil
}

func (m *modelCircuitBreakerTracker) OnOutboundRawError(ctx context.Context, err error) {
	if !m.shouldTrack() {
		return
	}
	if shouldSkipHealthStateTrackingForState(ctx, m.outbound.state) {
		return
	}
	if m.outbound.state.ModelCircuitBreakerRecorded {
		m.releaseProbeLease()
		return
	}

	// Capture whether this attempt was an active probe BEFORE releasing the lease,
	// so RecordError can decide whether to apply exponential backoff.
	wasProbe := m.probeActive
	m.releaseProbeLease()

	if errors.Is(err, context.Canceled) {
		m.outbound.state.ModelCircuitBreakerRecorded = true
		return
	}

	// Local skips never reached upstream — must not count as model errors.
	if errors.Is(err, errSkipCandidateByCircuitBreaker) || isChannelQueueError(err) {
		return
	}

	channel := m.outbound.GetCurrentChannel()
	modelID := m.outbound.GetRequestedModel()
	if channel == nil || modelID == "" {
		return
	}
	m.modelCircuitBreaker.RecordError(ctx, channel.ID, modelID, wasProbe)
	m.outbound.state.ModelCircuitBreakerRecorded = true
}

func (m *modelCircuitBreakerTracker) OnOutboundLlmStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*llm.Response], error) {
	if !m.shouldTrack() {
		return stream, nil
	}
	if shouldSkipHealthStateTrackingForState(ctx, m.outbound.state) {
		return stream, nil
	}
	channel := m.outbound.GetCurrentChannel()
	modelID := m.outbound.GetRequestedModel()
	if channel == nil || modelID == "" {
		return stream, nil
	}
	return &probeReleasingStream{
		ctx:       ctx,
		stream:    stream,
		state:     m.outbound.state,
		channelID: channel.ID,
		modelID:   modelID,
		wasProbe:  m.probeActive,
		release: func() {
			if m.outbound != nil {
				m.releaseProbeLease()
			}
		},
		released:            false,
		recorded:            false,
		modelCircuitBreaker: m.modelCircuitBreaker,
	}, nil
}

func (m *modelCircuitBreakerTracker) shouldTrack() bool {
	return m.outbound != nil &&
		m.outbound.state != nil &&
		m.outbound.state.RoutingPolicy.LoadBalancerStrategy == biz.LoadBalancerStrategyCircuitBreaker &&
		m.modelCircuitBreaker != nil
}

func (m *modelCircuitBreakerTracker) releaseProbeLease() {
	if m.outbound == nil || m.outbound.state == nil || m.modelCircuitBreaker == nil {
		return
	}

	if !m.probeActive {
		return
	}

	m.modelCircuitBreaker.EndProbe(m.probeChannelID, m.probeModelID)
	m.probeActive = false
}

//nolint:containedctx // Checked.
type probeReleasingStream struct {
	ctx            context.Context
	stream         streams.Stream[*llm.Response]
	state          *PersistenceState
	release        func()
	released       bool
	recorded       bool
	successPending bool

	modelCircuitBreaker *biz.ModelCircuitBreaker
	channelID           int
	modelID             string
	wasProbe            bool
}

func (s *probeReleasingStream) Next() bool {
	hasNext := s.stream.Next()
	if hasNext || s.recorded || s.modelCircuitBreaker == nil {
		return hasNext
	}

	if err := s.stream.Err(); err != nil {
		if !errors.Is(err, context.Canceled) {
			s.modelCircuitBreaker.RecordError(s.ctx, s.channelID, s.modelID, s.wasProbe)
		}
		s.recorded = true
		s.state.ModelCircuitBreakerRecorded = true
	} else if s.successPending {
		s.modelCircuitBreaker.RecordSuccess(s.ctx, s.channelID, s.modelID)
		s.recorded = true
		s.state.ModelCircuitBreakerRecorded = true
	}

	return hasNext
}

func (s *probeReleasingStream) Current() *llm.Response {
	event := s.stream.Current()
	if event == nil {
		return nil
	}

	if s.modelCircuitBreaker == nil {
		return event
	}

	if !s.recorded {
		switch event.TerminalOutcome() {
		case llm.ResponseTerminalOutcomeFailed, llm.ResponseTerminalOutcomeIncomplete:
			s.modelCircuitBreaker.RecordError(s.ctx, s.channelID, s.modelID, s.wasProbe)
			s.recorded = true
			s.state.ModelCircuitBreakerRecorded = true
		case llm.ResponseTerminalOutcomeCanceled:
			s.recorded = true
			s.state.ModelCircuitBreakerRecorded = true
		default:
			if tokenCount := event.Usage.GetCompletionTokens(); tokenCount != nil && *tokenCount > 0 {
				s.successPending = true
			}
		}
	}

	return event
}

func (s *probeReleasingStream) Err() error {
	return s.stream.Err()
}

func (s *probeReleasingStream) Close() error {
	if !s.released && s.release != nil {
		s.released = true
		s.release()
	}

	return s.stream.Close()
}

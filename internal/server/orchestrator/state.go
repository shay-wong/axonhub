package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// PersistenceState holds shared state with channel management and retry capabilities.
// TODO: move the dependencies out of the state to make it a real state.
type PersistenceState struct {
	APIKey *ent.APIKey

	RequestService      *biz.RequestService
	UsageLogService     *biz.UsageLogService
	SystemService       *biz.SystemService
	ChannelService      *biz.ChannelService
	PromptProvider      PromptProvider
	PromptProtecter     PromptProtecter
	RetryPolicyProvider RetryPolicyProvider
	RetryPolicy         *biz.RetryPolicy
	CandidateSelector   CandidateSelector
	LoadBalancers       map[string]*LoadBalancer
	RoutingPolicy       EffectiveRoutingPolicy

	// Request state
	ModelMapper *ModelMapper
	// Proxy config, will be used to override channel's default proxy config.
	Proxy *httpclient.ProxyConfig

	// OriginalModel is the model after API key profile mapping, used for channel selection
	OriginalModel string
	RawRequest    *httpclient.Request
	LlmRequest    *llm.Request

	// OriginalRequestStream stores the client's original stream intent before any
	// candidate-specific forcing to provider-side streaming happens.
	OriginalRequestStream *bool

	// Persistence state
	Request     *ent.Request
	RequestExec *ent.RequestExecution

	// ChannelModelsCandidates is the primary state for channel selection
	ChannelModelsCandidates []*ChannelModelsCandidate
	// Candidate state - current candidate index of ChannelModelsCandidates
	CurrentCandidateIndex int
	// CurrentCandidate is the currently selected candidate of ChannelModelsCandidates
	CurrentCandidate *ChannelModelsCandidate
	// CurrentModelIndex is the current model index in CurrentCandidate.Models
	CurrentModelIndex int

	// Perf records channel health and latency for the current request. For stream
	// terminals, incomplete (token limit or content filtering) counts as channel
	// success: it must not trigger channel failure counts or auto-disable rules.
	Perf *biz.PerformanceRecord
	// ModelCircuitBreakerRecorded prevents one provider attempt from updating
	// circuit-breaker state more than once across stream and raw-error hooks.
	ModelCircuitBreakerRecorded bool

	// StreamCompleted tracks a successful generation outcome, not channel health.
	// A recognized incomplete terminal keeps this false and is persisted as Failed
	// on both request and execution, even though Perf.Success is true. This does
	// not turn a valid protocol response (such as finish_reason=length) into an
	// HTTP/stream error for the client.
	StreamCompleted bool
	// StreamTerminalError records an application-level terminal stream event such
	// as response.failed, response.incomplete, response.cancelled, or error.
	StreamTerminalError error
	// StreamTerminalBody preserves the first provider terminal event for error
	// paths that return before the inbound persistence wrapper is installed.
	StreamTerminalBody []byte

	// OutboundStreamTerminal preserves the provider outcome across protocol
	// conversion, which may replace an abnormal terminal event with a generic stop.
	// Request/execution status and channel health intentionally interpret this
	// outcome differently, as described by StreamCompleted and Perf above.
	OutboundStreamTerminal streamTerminalState

	// RawProviderResponse stores the raw provider response for non-stream response pass-through.
	RawProviderResponse *httpclient.Response

	// RawProviderRequest stores the actual outbound provider request for pass-through checks.
	RawProviderRequest *httpclient.Request

	// RequestedServiceTier is the service tier in the final orchestrator request for
	// the current provider attempt, after registered request mutators are applied.
	// Provider executor implementations may still perform protocol-specific rewrites.
	RequestedServiceTier string

	// AppliedServiceTier is the actual tier reported by the provider response.
	// It remains empty when the provider does not report a tier and is kept separate
	// from the request-derived pricing decision.
	AppliedServiceTier string

	// RequestPricingOverride is a provider-specific price key derived from the
	// final outbound request when the response tier is unreliable or unrelated.
	RequestPricingOverride string
	// RequestPricingOverridePolicy controls whether the request-derived price key
	// replaces only default responses or every provider-applied tier.
	RequestPricingOverridePolicy biz.RequestPricingOverridePolicy

	// SpeedMode is a provider-independent display mode derived from the final
	// outbound request, such as OpenAI priority or Anthropic fast.
	SpeedMode string

	// UsageLogEligible is true only after the pipeline accepts the current
	// streaming attempt as the request response.
	UsageLogEligible bool

	// RawStreamCh receives raw provider stream events for stream response pass-through.
	RawStreamCh chan *httpclient.StreamEvent

	// RawStreamErrRef points to the current attempt's local error variable used by the
	// captureRawProviderStream fan-out goroutine. Using a per-attempt pointer (instead of
	// a single shared field) prevents data races when retries spawn a new goroutine before
	// the previous one has exited.
	RawStreamErrRef *error

	// RawStreamCancel cancels the current attempt's fan-out goroutine started by
	// captureRawProviderStream. Must be called in PrepareForRetry and NextChannel so the
	// abandoned goroutine exits promptly and releases its upstream HTTP connection.
	RawStreamCancel context.CancelFunc

	// PassThroughApplied records whether the inbound request body was substituted during pass-through.
	PassThroughApplied bool
}

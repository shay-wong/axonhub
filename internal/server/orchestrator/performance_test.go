package orchestrator

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

type responseEventsThenError struct {
	events []*llm.Response
	index  int
	err    error
}

func (s *responseEventsThenError) Next() bool {
	if s.index >= len(s.events) {
		return false
	}
	s.index++

	return true
}

func (s *responseEventsThenError) Current() *llm.Response {
	return s.events[s.index-1]
}

func (s *responseEventsThenError) Err() error {
	if s.index >= len(s.events) {
		return s.err
	}

	return nil
}

func (s *responseEventsThenError) Close() error { return nil }

var _ streams.Stream[*llm.Response] = (*responseEventsThenError)(nil)

type transportErrorRoundTripper struct {
	err error
}

func (r transportErrorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, r.err
}

type streamErrorRoundTripper struct{}

func (streamErrorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &streamBodyThenError{
			data: []byte("data: {\"type\":\"test\"}\n\n"),
			err:  errors.New("http2: stream reset"),
		},
	}, nil
}

type streamBodyThenError struct {
	data []byte
	err  error
	read bool
}

func (r *streamBodyThenError) Read(p []byte) (int, error) {
	if r.read {
		return 0, r.err
	}

	r.read = true

	return copy(p, r.data), nil
}

func (r *streamBodyThenError) Close() error { return nil }

// mockChannelService is a mock implementation of ChannelService for testing
type mockChannelService struct{}

func (m *mockChannelService) AsyncRecordPerformance(ctx context.Context, perf *biz.PerformanceRecord) {
	// No-op for testing
}

// TestPerformanceRecording_OnInboundLlmRequest_SetsStreamFlag verifies that
// the Stream flag is correctly set based on the request's Stream field.
func TestPerformanceRecording_OnInboundLlmRequest_SetsStreamFlag(t *testing.T) {
	tests := []struct {
		name         string
		streamValue  *bool
		expectedFlag bool
	}{
		{
			name:         "streaming request - Stream is true",
			streamValue:  new(true),
			expectedFlag: true,
		},
		{
			name:         "non-streaming request - Stream is false",
			streamValue:  new(false),
			expectedFlag: false,
		},
		{
			name:         "nil stream value defaults to false",
			streamValue:  nil,
			expectedFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			state := &PersistenceState{
				Perf: nil,
			}
			outbound := &PersistentOutboundTransformer{
				state: state,
			}
			middleware := &performanceRecording{
				outbound: outbound,
			}

			request := &llm.Request{
				Model:  "gpt-4",
				Stream: tt.streamValue,
			}
			ctx := context.Background()

			// Execute
			result, err := middleware.OnInboundLlmRequest(ctx, request)

			// Assert
			require.NoError(t, err)
			assert.Equal(t, request, result)
			assert.NotNil(t, state.Perf)
			assert.Equal(t, tt.expectedFlag, state.Perf.Stream)
		})
	}
}

func TestRecordTerminalOutcomePerformance(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	channelService, _, _, _ := setupTestServices(t, client)

	tests := []struct {
		name       string
		response   *llm.Response
		wantCode   int
		wantCancel bool
	}{
		{
			name: "failed",
			response: &llm.Response{
				ProviderTerminalOutcome: llm.ResponseTerminalOutcomeFailed,
				Error:                   &llm.ResponseError{StatusCode: http.StatusServiceUnavailable},
			},
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:     "incomplete",
			response: &llm.Response{ProviderTerminalOutcome: llm.ResponseTerminalOutcomeIncomplete},
		},
		{
			name:       "canceled",
			response:   &llm.Response{ProviderTerminalOutcome: llm.ResponseTerminalOutcomeCanceled},
			wantCancel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perf := &biz.PerformanceRecord{ChannelID: 1, StartTime: time.Now()}
			state := &PersistenceState{Perf: perf, ChannelService: channelService}

			require.True(t, recordTerminalOutcomePerformance(t.Context(), state, tt.response))
			require.True(t, perf.RequestCompleted)
			require.False(t, perf.Success)
			require.Equal(t, tt.wantCode, perf.ResponseStatusCode)
			require.Equal(t, tt.wantCancel, perf.Canceled)
		})
	}
}

func TestRecordPerformanceErrorClassifiesTransportFailure(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	channelService, _, _, _ := setupTestServices(t, client)

	tests := []struct {
		name          string
		err           error
		wantTransport bool
		wantCode      int
	}{
		{name: "upstream EOF", err: io.EOF, wantTransport: true, wantCode: http.StatusInternalServerError},
		{name: "stream first event timeout", err: pipeline.ErrStreamFirstEventTimeout, wantTransport: true, wantCode: http.StatusInternalServerError},
		{name: "non-stream response timeout", err: pipeline.ErrNonStreamResponseTimeout, wantTransport: true, wantCode: http.StatusInternalServerError},
		{name: "upstream HTTP 500", err: &httpclient.Error{StatusCode: http.StatusInternalServerError}, wantCode: http.StatusInternalServerError},
		{
			name: "upstream HTTP status with response body error",
			err: &httpclient.Error{
				StatusCode: http.StatusTooManyRequests,
				Err:        io.ErrUnexpectedEOF,
			},
			wantCode: http.StatusTooManyRequests,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perf := &biz.PerformanceRecord{ChannelID: 1, StartTime: time.Now()}
			state := &PersistenceState{Perf: perf, ChannelService: channelService}

			require.True(t, recordPerformanceError(t.Context(), state, tt.err))
			require.Equal(t, tt.wantTransport, perf.TransportFailure)
			require.Equal(t, tt.wantCode, perf.ResponseStatusCode)
		})
	}
}

func TestCredentialAgnosticFailuresDoNotDisableAPIKey(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := ent.NewContext(authz.WithTestBypass(t.Context()), client)
	channelService := biz.NewChannelServiceForTest(client)
	defer channelService.Stop()
	require.NoError(t, channelService.SystemService.SetRetryPolicy(ctx, &biz.RetryPolicy{
		APIKeyAutoDisable: biz.AutoDisablePolicy{
			Enabled: true,
			Statuses: []biz.AutoDisableStatusRule{
				{Status: http.StatusInternalServerError, Times: 1, Action: biz.DisableActionPermanent},
			},
		},
	}))

	channelRow, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Transport Failure Channel").
		SetBaseURL("https://upstream.invalid").
		SetCredentials(objects.ChannelCredentials{APIKeys: []string{"key1", "key2"}}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	causes := []error{
		&net.DNSError{Err: "no such host", Name: "upstream.invalid", IsNotFound: true},
		&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
	}
	requestErrors := make([]error, 0, len(causes)+1)
	for _, cause := range causes {
		httpClient := httpclient.NewHttpClientWithClient(&http.Client{
			Transport: transportErrorRoundTripper{err: cause},
		})
		_, requestErr := httpClient.Do(ctx, &httpclient.Request{
			Method: http.MethodGet,
			URL:    channelRow.BaseURL,
		})
		require.Error(t, requestErr)
		requestErrors = append(requestErrors, requestErr)
	}

	httpClient := httpclient.NewHttpClientWithClient(&http.Client{Transport: streamErrorRoundTripper{}})
	stream, err := httpClient.DoStream(ctx, &httpclient.Request{
		Method: http.MethodGet,
		URL:    channelRow.BaseURL,
	})
	require.NoError(t, err)
	require.True(t, stream.Next())
	require.NotNil(t, stream.Current())
	require.False(t, stream.Next())
	require.Error(t, stream.Err())
	require.True(t, httpclient.IsTransportError(stream.Err()))
	requestErrors = append(requestErrors, stream.Err())
	requestErrors = append(requestErrors,
		pipeline.ErrStreamFirstEventTimeout,
		pipeline.ErrNonStreamResponseTimeout,
	)

	for _, requestErr := range requestErrors {
		state := &PersistenceState{
			Perf: &biz.PerformanceRecord{
				ChannelID: channelRow.ID,
				APIKey:    "key1",
				StartTime: time.Now(),
			},
			ChannelService: channelService,
		}
		require.True(t, recordPerformanceError(ctx, state, requestErr))
		require.True(t, state.Perf.TransportFailure)
	}

	require.Eventually(t, func() bool {
		metrics, metricsErr := channelService.GetChannelMetrics(ctx, channelRow.ID)
		return metricsErr == nil && metrics.FailureCount == int64(len(requestErrors))
	}, time.Second, 10*time.Millisecond)

	updatedChannel, err := client.Channel.Get(ctx, channelRow.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedChannel.Status)
	require.Empty(t, updatedChannel.DisabledAPIKeys)
}

func TestUsageBeforeLateStreamErrorDoesNotRecordSuccess(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()
	channelService, _, _, _ := setupTestServices(t, client)
	channel := &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "test-channel"}, Outbound: &mockTransformer{}}
	state := &PersistenceState{
		Perf:           &biz.PerformanceRecord{ChannelID: channel.ID, StartTime: time.Now(), Stream: true},
		ChannelService: channelService,
		OriginalModel:  "gpt-5",
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: channel,
		},
	}
	lateErr := errors.New("stream failed after usage")
	source := &responseEventsThenError{
		events: []*llm.Response{{Usage: &llm.Usage{CompletionTokens: 3, TotalTokens: 3}}},
		err:    lateErr,
	}
	performanceStream := &recordPerformanceStream{ctx: t.Context(), stream: source, state: state}
	modelCircuitBreaker := biz.NewModelCircuitBreaker()
	circuitStream := &probeReleasingStream{
		ctx:                 t.Context(),
		stream:              performanceStream,
		state:               state,
		modelCircuitBreaker: modelCircuitBreaker,
		channelID:           channel.ID,
		modelID:             "gpt-5",
	}

	for circuitStream.Next() {
		_ = circuitStream.Current()
	}
	require.ErrorIs(t, circuitStream.Err(), lateErr)
	require.True(t, state.Perf.RequestCompleted)
	require.False(t, state.Perf.Success)
	stats := modelCircuitBreaker.GetModelCircuitBreakerStats(t.Context(), channel.ID, "gpt-5")
	require.NotNil(t, stats)
	require.Equal(t, 1, stats.ConsecutiveFailures)

	outbound := &PersistentOutboundTransformer{state: state}
	tracker := &modelCircuitBreakerTracker{outbound: outbound, modelCircuitBreaker: modelCircuitBreaker}
	tracker.OnOutboundRawError(t.Context(), lateErr)
	(&performanceRecording{outbound: outbound}).OnOutboundRawError(t.Context(), lateErr)

	require.True(t, state.Perf.RequestCompleted)
	require.False(t, state.Perf.Success)
	stats = modelCircuitBreaker.GetModelCircuitBreakerStats(t.Context(), channel.ID, "gpt-5")
	require.NotNil(t, stats)
	require.Equal(t, 1, stats.ConsecutiveFailures)
}

// TestPerformanceRecording_OnOutboundRawRequest_PreservesStreamFlag verifies that
// the Stream flag set in OnInboundLlmRequest is preserved when OnOutboundRawRequest
// creates a new PerformanceRecord. This test would FAIL if the bug were reverted.
func TestPerformanceRecording_OnOutboundRawRequest_PreservesStreamFlag(t *testing.T) {
	tests := []struct {
		name              string
		initialStreamFlag bool
		expectedStream    bool
	}{
		{
			name:              "streaming request preserves Stream=true",
			initialStreamFlag: true,
			expectedStream:    true,
		},
		{
			name:              "non-streaming request preserves Stream=false",
			initialStreamFlag: false,
			expectedStream:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:   1,
					Name: "test-channel",
				},
				Outbound: &mockTransformer{},
			}

			// Simulate that OnInboundLlmRequest already set the Stream flag
			state := &PersistenceState{
				Perf: &biz.PerformanceRecord{
					Stream: tt.initialStreamFlag,
				},
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
				},
			}
			outbound := &PersistentOutboundTransformer{
				state: state,
			}
			middleware := &performanceRecording{
				outbound: outbound,
			}

			request := &httpclient.Request{
				Method: "POST",
				URL:    "https://api.example.com/v1/chat/completions",
			}
			ctx := context.Background()

			// Execute - this is where the bug would cause Stream flag to be lost
			result, err := middleware.OnOutboundRawRequest(ctx, request)

			// Assert
			require.NoError(t, err)
			assert.Equal(t, request, result)
			assert.NotNil(t, state.Perf)

			// CRITICAL: This assertion verifies the fix. If the bug were reverted
			// (i.e., creating a new PerformanceRecord without preserving Stream),
			// this test would FAIL.
			assert.Equal(t, tt.expectedStream, state.Perf.Stream,
				"Stream flag should be preserved from OnInboundLlmRequest through OnOutboundRawRequest. "+
					"If this fails, the bug has been reverted!")
		})
	}
}

func TestPerformanceRecording_OnOutboundRawRequest_SkipsHealthStateTrackingForTestSource(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: channel,
		},
	}
	outbound := &PersistentOutboundTransformer{
		state: state,
	}
	middleware := &performanceRecording{
		outbound: outbound,
	}

	ctx := contexts.WithSource(context.Background(), request.SourceTest)
	req := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
	}

	result, err := middleware.OnOutboundRawRequest(ctx, req)
	require.NoError(t, err)
	require.Equal(t, req, result)
	require.NotNil(t, state.Perf)
	require.True(t, state.Perf.SkipHealthStateTracking)
}

func TestPerformanceRecording_OnOutboundRawRequest_SkipsHealthStateTrackingForPersistedTestRequest(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	state := &PersistenceState{
		Request: &ent.Request{
			Source: request.SourceTest,
		},
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: channel,
		},
	}
	outbound := &PersistentOutboundTransformer{
		state: state,
	}
	middleware := &performanceRecording{
		outbound: outbound,
	}

	req := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
	}

	result, err := middleware.OnOutboundRawRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, req, result)
	require.NotNil(t, state.Perf)
	require.True(t, state.Perf.SkipHealthStateTracking)
}

func TestModelCircuitBreakerTracker_OnOutboundRawError_SkipsHealthStateTrackingForPersistedTestRequest(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}
	modelCircuitBreaker := biz.NewModelCircuitBreaker()
	modelID := "gpt-4"
	state := &PersistenceState{
		Request: &ent.Request{
			Source: request.SourceTest,
		},
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: channel,
			Models: []biz.ChannelModelEntry{
				{RequestModel: modelID, ActualModel: modelID},
			},
		},
		CurrentModelIndex: 0,
	}
	tracker := &modelCircuitBreakerTracker{
		outbound: &PersistentOutboundTransformer{
			state: state,
		},
		modelCircuitBreaker: modelCircuitBreaker,
		strategy:            biz.LoadBalancerStrategyCircuitBreaker,
	}

	tracker.OnOutboundRawError(context.Background(), &httpclient.Error{StatusCode: http.StatusServiceUnavailable})

	stats := modelCircuitBreaker.GetModelCircuitBreakerStats(context.Background(), channel.ID, modelID)
	require.NotNil(t, stats)
	require.Equal(t, biz.StateClosed, stats.State)
	require.Equal(t, 0, stats.ConsecutiveFailures)
	require.True(t, stats.LastFailureAt.IsZero())
}

func TestTestChannelOrchestrator_TestChannel_SourceTestSkipsHealthStateTracking(t *testing.T) {
	ctx := context.Background()
	ctx = authz.WithTestBypass(ctx)

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx = ent.NewContext(ctx, client)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
	}))
	defer upstream.Close()

	channelRow, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("Test Source Channel").
		SetBaseURL(upstream.URL).
		SetCredentials(objects.ChannelCredentials{APIKey: "test-api-key"}).
		SetSupportedModels([]string{"gpt-4"}).
		SetDefaultTestModel("gpt-4").
		SetStatus(channel.StatusEnabled).
		Save(ctx)
	require.NoError(t, err)

	channelService, requestService, systemService, usageLogService := setupTestServices(t, client)
	require.NoError(t, systemService.SetRetryPolicy(ctx, &biz.RetryPolicy{
		Enabled: false,
		ChannelAutoDisable: biz.AutoDisablePolicy{
			Enabled: true,
			Statuses: []biz.AutoDisableStatusRule{
				{Status: http.StatusServiceUnavailable, Times: 1, Action: biz.DisableActionPermanent},
			},
		},
		APIKeyAutoDisable: biz.AutoDisablePolicy{
			Enabled: true,
			Statuses: []biz.AutoDisableStatusRule{
				{Status: http.StatusServiceUnavailable, Times: 1, Action: biz.DisableActionPermanent},
			},
		},
	}))

	promptProtectionRuleService := biz.NewPromptProtectionRuleService(biz.PromptProtectionRuleServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})
	defer promptProtectionRuleService.Stop()

	tester := NewTestChannelOrchestrator(
		channelService,
		requestService,
		systemService,
		usageLogService,
		promptProtectionRuleService,
		httpclient.NewHttpClientWithClient(upstream.Client()),
	)

	result, err := tester.TestChannel(contexts.WithSource(ctx, request.SourceTest), objects.GUID{ID: channelRow.ID}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Success)

	require.Eventually(t, func() bool {
		count, err := client.RequestExecution.Query().Count(ctx)
		if err != nil {
			return false
		}

		return count == 1
	}, time.Second, 10*time.Millisecond)

	metrics, err := channelService.GetChannelMetrics(ctx, channelRow.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), metrics.RequestCount)
	require.Equal(t, int64(0), metrics.FailureCount)
	require.Equal(t, int64(0), metrics.ConsecutiveFailures)
	require.Nil(t, metrics.LastFailureAt)

	updatedCh, err := client.Channel.Get(ctx, channelRow.ID)
	require.NoError(t, err)
	require.Equal(t, channel.StatusEnabled, updatedCh.Status)
	require.Empty(t, updatedCh.DisabledAPIKeys)
}

// TestPerformanceRecording_FullLifecycle_StreamFlagPreserved tests the complete
// middleware lifecycle to ensure Stream flag is preserved end-to-end.
func TestPerformanceRecording_FullLifecycle_StreamFlagPreserved(t *testing.T) {
	// Setup
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	state := &PersistenceState{
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: channel,
		},
	}
	outbound := &PersistentOutboundTransformer{
		state: state,
	}
	middleware := &performanceRecording{
		outbound: outbound,
	}

	ctx := context.Background()
	streamValue := true

	// Step 1: Inbound request processing
	llmRequest := &llm.Request{
		Model:  "gpt-4",
		Stream: &streamValue,
	}
	_, err := middleware.OnInboundLlmRequest(ctx, llmRequest)
	require.NoError(t, err)
	require.NotNil(t, state.Perf)
	assert.True(t, state.Perf.Stream, "Stream flag should be true after OnInboundLlmRequest")

	// Step 2: Outbound request processing (this is where the bug occurred)
	httpRequest := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
	}
	_, err = middleware.OnOutboundRawRequest(ctx, httpRequest)
	require.NoError(t, err)
	require.NotNil(t, state.Perf)

	// CRITICAL: Stream flag must still be true after OnOutboundRawRequest
	assert.True(t, state.Perf.Stream,
		"Stream flag should be preserved through OnOutboundRawRequest. "+
			"This assertion would FAIL if the bug (8afd95c3) were reverted.")
}

// TestPerformanceRecording_OnOutboundRawRequest_NoExistingPerf verifies that
// OnOutboundRawRequest works correctly even if OnInboundLlmRequest wasn't called
// (edge case where Perf is nil).
func TestPerformanceRecording_OnOutboundRawRequest_NoExistingPerf(t *testing.T) {
	// Setup
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	state := &PersistenceState{
		Perf: nil, // No existing PerformanceRecord
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: channel,
		},
	}
	outbound := &PersistentOutboundTransformer{
		state: state,
	}
	middleware := &performanceRecording{
		outbound: outbound,
	}

	request := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
	}
	ctx := context.Background()

	// Execute
	result, err := middleware.OnOutboundRawRequest(ctx, request)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, request, result)
	assert.NotNil(t, state.Perf)
	assert.False(t, state.Perf.Stream, "Stream should default to false when no existing Perf")
}

// TestPerformanceRecording_OnOutboundRawRequest_NoChannel verifies that
// OnOutboundRawRequest returns early when there's no channel.
func TestPerformanceRecording_OnOutboundRawRequest_NoChannel(t *testing.T) {
	// Setup
	state := &PersistenceState{
		Perf:             &biz.PerformanceRecord{Stream: true},
		CurrentCandidate: nil, // No channel
	}
	outbound := &PersistentOutboundTransformer{
		state: state,
	}
	middleware := &performanceRecording{
		outbound: outbound,
	}

	request := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
	}
	ctx := context.Background()

	// Execute
	result, err := middleware.OnOutboundRawRequest(ctx, request)

	// Assert - should return early without modifying Perf
	require.NoError(t, err)
	assert.Equal(t, request, result)
	// Perf should remain unchanged since we returned early
	assert.True(t, state.Perf.Stream)
}

// TestPerformanceRecording_StreamFlagBugRegression is specifically designed to
// catch the regression if the fix is reverted. It documents the exact bug scenario.
func TestPerformanceRecording_StreamFlagBugRegression(t *testing.T) {
	// This test documents the bug introduced in commit 8afd95c3:
	// "feat: trace stikcy api key for multiple api keys channel"
	//
	// The bug: OnOutboundRawRequest() was creating a new PerformanceRecord without
	// preserving the Stream flag that was set in OnInboundLlmRequest().
	//
	// The fix: Preserve the Stream flag before creating the new PerformanceRecord.

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	// Simulate the state after OnInboundLlmRequest was called with stream=true
	state := &PersistenceState{
		Perf: &biz.PerformanceRecord{
			Stream: true, // Set by OnInboundLlmRequest
		},
		CurrentCandidate: &ChannelModelsCandidate{
			Channel: channel,
		},
	}
	outbound := &PersistentOutboundTransformer{
		state: state,
	}
	middleware := &performanceRecording{
		outbound: outbound,
	}

	ctx := context.Background()
	request := &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
	}

	// Execute OnOutboundRawRequest
	_, err := middleware.OnOutboundRawRequest(ctx, request)
	require.NoError(t, err)

	// The bug would cause this assertion to fail because:
	// 1. OnInboundLlmRequest set Perf.Stream = true
	// 2. OnOutboundRawRequest created a new PerformanceRecord{}
	// 3. The new PerformanceRecord had Stream = false (zero value)
	// 4. This overwrote the Perf pointer, losing the Stream flag
	//
	// The fix preserves Stream from the old Perf before creating the new one.
	assert.True(t, state.Perf.Stream,
		"BUG REGRESSION DETECTED: Stream flag was lost in OnOutboundRawRequest. "+
			"This indicates the fix from commit 8afd95c3 has been reverted.")
}

// TestRecordPerformanceStream_MarksFirstToken verifies that recordPerformanceStream
// correctly marks the first token time.

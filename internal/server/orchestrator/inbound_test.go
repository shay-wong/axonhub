package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

// mockInboundTransformer is a mock transformer for testing.
type mockInboundTransformer struct {
	aggregateResponseBody []byte
	aggregateMeta         llm.ResponseMeta
	aggregateErr          error
	transformedError      *httpclient.Error
}

func (m *mockInboundTransformer) APIFormat() llm.APIFormat {
	return llm.APIFormatOpenAIChatCompletion
}

func (m *mockInboundTransformer) TransformRequest(ctx context.Context, request *httpclient.Request) (*llm.Request, error) {
	return &llm.Request{}, nil
}

func (m *mockInboundTransformer) TransformResponse(ctx context.Context, response *llm.Response) (*httpclient.Response, error) {
	return &httpclient.Response{}, nil
}

func (m *mockInboundTransformer) TransformStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return nil, nil
}

func (m *mockInboundTransformer) TransformError(ctx context.Context, rawErr error) *httpclient.Error {
	return m.transformedError
}

func (m *mockInboundTransformer) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return m.aggregateResponseBody, m.aggregateMeta, m.aggregateErr
}

// mockStream is a simple mock stream for testing.
type mockStream struct {
	events     []*httpclient.StreamEvent
	currentIdx int
	closed     bool
	err        error
}

func (m *mockStream) Next() bool {
	if m.currentIdx >= len(m.events) {
		return false
	}
	m.currentIdx++
	return true
}

func (m *mockStream) Current() *httpclient.StreamEvent {
	if m.currentIdx > len(m.events) {
		return nil
	}
	return m.events[m.currentIdx-1]
}

func (m *mockStream) Err() error {
	return m.err
}

func (m *mockStream) Close() error {
	m.closed = true
	return nil
}

// createTestRequestService creates a minimal request service for testing.
func createTestRequestService(t *testing.T, client *ent.Client) *biz.RequestService {
	t.Helper()

	systemService := biz.NewSystemService(biz.SystemServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})

	dataStorageService := &biz.DataStorageService{
		AbstractService: &biz.AbstractService{},
		SystemService:   systemService,
		Cache:           xcache.NewFromConfig[ent.DataStorage](xcache.Config{Mode: xcache.ModeMemory}),
	}
	liveStreamRegistry := biz.NewLiveStreamRegistry()

	channelService := biz.NewChannelServiceForTest(client)
	usageLogService := biz.NewUsageLogService(client, systemService, channelService)

	return biz.NewRequestService(client, systemService, usageLogService, dataStorageService, liveStreamRegistry)
}

// newInboundPersistentStreamHelper creates a configured InboundPersistentStream for testing.
// It encapsulates common setup: ent.Client, context, RequestService, test request/execution, and persistence state.
// The caller provides the mock stream and transformer to test specific behaviors.
func newInboundPersistentStreamHelper(
	t *testing.T,
	mockStream streams.Stream[*httpclient.StreamEvent],
	mockTransformer transformer.Inbound,
) (*InboundPersistentStream, *ent.Client, context.Context, *PersistenceState) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)

	requestService := createTestRequestService(t, client)

	testRequest := &ent.Request{ID: 1}
	testRequestExec := &ent.RequestExecution{ID: 1}

	state := &PersistenceState{StreamCompleted: false}

	stream := NewInboundPersistentStream(
		ctx,
		mockStream,
		testRequest,
		testRequestExec,
		requestService,
		mockTransformer,
		nil,
		state,
	)

	return stream, client, ctx, state
}

// TestInboundPersistentStream_Close_WithCompleteResponse tests the NEW behavior:
// complete response without terminal event (e.g., Codex executor that aggregates internally)
func TestInboundPersistentStream_Close_WithCompleteResponse(t *testing.T) {
	completeResponseChunk := &httpclient.StreamEvent{
		Type: "chunk",
		Data: []byte(`{"id":"chatcmpl-abc123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
	}

	mockStream := &mockStream{
		events: []*httpclient.StreamEvent{completeResponseChunk},
	}

	mockTransformer := &mockInboundTransformer{
		aggregateResponseBody: []byte(`{"id":"chatcmpl-abc123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
		aggregateMeta: llm.ResponseMeta{
			ID: "chatcmpl-abc123",
			Usage: &llm.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		aggregateErr: nil,
	}

	stream, client, _, state := newInboundPersistentStreamHelper(t, mockStream, mockTransformer)
	defer client.Close()

	require.True(t, stream.Next(), "Expected Next() to return true")
	event := stream.Current()
	require.NotNil(t, event, "Expected current event to not be nil")

	assert.False(t, state.StreamCompleted, "StreamCompleted should be false before Close()")

	err := stream.Close()
	require.NoError(t, err, "Close() should not return an error")

	assert.True(t, state.StreamCompleted, "StreamCompleted should be true after Close() with complete response")
	assert.True(t, mockStream.closed, "Stream should be closed")
}

// TestInboundPersistentStream_Close_WithTerminalEvent tests the EXISTING behavior:
// terminal event (e.g., [DONE] event from OpenAI)
func TestInboundPersistentStream_Close_WithTerminalEvent(t *testing.T) {
	regularResponseChunk := &httpclient.StreamEvent{
		Type: "chunk",
		Data: []byte(`{"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`),
	}

	doneEvent := &httpclient.StreamEvent{
		Data: []byte("[DONE]"),
	}

	mockStream := &mockStream{
		events: []*httpclient.StreamEvent{regularResponseChunk, doneEvent},
	}

	mockTransformer := &mockInboundTransformer{
		aggregateResponseBody: []byte(`{"id":"chatcmpl-abc123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
		aggregateMeta: llm.ResponseMeta{
			ID: "chatcmpl-abc123",
			Usage: &llm.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		},
		aggregateErr: nil,
	}

	stream, client, _, state := newInboundPersistentStreamHelper(t, mockStream, mockTransformer)
	defer client.Close()

	require.True(t, stream.Next(), "Expected Next() to return true for first chunk")
	_ = stream.Current()

	require.True(t, stream.Next(), "Expected Next() to return true for [DONE] event")
	event := stream.Current()
	require.NotNil(t, event, "Expected current event to not be nil")

	assert.True(t, state.StreamCompleted, "StreamCompleted should be true after [DONE] event")

	err := stream.Close()
	require.NoError(t, err, "Close() should not return an error")

	assert.True(t, state.StreamCompleted, "StreamCompleted should remain true after Close()")
	assert.True(t, mockStream.closed, "Stream should be closed")
}

// TestInboundPersistentStream_Close_WithAggregationError tests the error path:
// aggregation fails but fallback behavior still works (persistResponseChunks called in final block).
func TestInboundPersistentStream_Close_WithAggregationError(t *testing.T) {
	regularResponseChunk := &httpclient.StreamEvent{
		Type: "chunk",
		Data: []byte(`{"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`),
	}

	mockStream := &mockStream{
		events: []*httpclient.StreamEvent{regularResponseChunk},
	}

	mockTransformer := &mockInboundTransformer{
		aggregateResponseBody: nil,
		aggregateMeta:         llm.ResponseMeta{},
		aggregateErr:          errors.New("aggregation failed"),
	}

	stream, client, _, state := newInboundPersistentStreamHelper(t, mockStream, mockTransformer)
	defer client.Close()

	require.True(t, stream.Next(), "Expected Next() to return true for first chunk")
	_ = stream.Current()

	assert.False(t, state.StreamCompleted, "StreamCompleted should be false before Close()")

	err := stream.Close()
	require.NoError(t, err, "Close() should not return an error")

	assert.False(t, state.StreamCompleted, "StreamCompleted should remain false after Close() with aggregation error")
	assert.True(t, mockStream.closed, "Stream should be closed")
}

func TestInboundPersistentStream_Close_PersistsResponseFailure(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	project := createTestProject(t, ctx, client)
	ch := createTestChannel(t, ctx, client)
	_, requestService, systemService, _ := setupTestServices(t, client)
	require.NoError(t, systemService.SetStoragePolicy(ctx, &biz.StoragePolicy{
		StoreChunks:       true,
		StoreResponseBody: true,
	}))

	req, err := client.Request.Create().
		SetProjectID(project.ID).
		SetChannelID(ch.ID).
		SetModelID("gpt-4.1").
		SetStatus(request.StatusPending).
		SetRequestBody([]byte(`{"stream":true}`)).
		Save(ctx)
	require.NoError(t, err)

	failedBody := []byte(`{
		"id":"resp_failed",
		"status":"failed",
		"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"context window exceeded"},
		"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}
	}`)
	state := &PersistenceState{}
	stream := NewInboundPersistentStream(
		ctx,
		&mockStream{events: []*httpclient.StreamEvent{{
			Type: "response.failed",
			Data: []byte(`{
				"type":"response.failed",
				"response":{
					"id":"resp_failed",
					"status":"failed",
					"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"context window exceeded"},
					"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}
				}
			}`),
		}}},
		req,
		nil,
		requestService,
		&mockInboundTransformer{
			aggregateResponseBody: failedBody,
			aggregateMeta: llm.ResponseMeta{
				ID:    "resp_failed",
				Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
			},
		},
		nil,
		state,
	)
	for stream.Next() {
		_ = stream.Current()
	}
	require.NoError(t, stream.Close())

	dbReq, err := client.Request.Get(ctx, req.ID)
	require.NoError(t, err)
	require.Equal(t, request.StatusFailed, dbReq.Status)
	require.JSONEq(t, string(failedBody), string(dbReq.ResponseBody))
	require.NotEmpty(t, dbReq.ResponseChunks)
	require.False(t, state.StreamCompleted)
	require.Error(t, state.StreamTerminalError)
	require.Contains(t, state.StreamTerminalError.Error(), "context_length_exceeded")
}

func TestInboundPersistentStream_Close_PersistsTransformedTerminalErrorWithoutClientEnvelope(t *testing.T) {
	tests := []struct {
		name           string
		events         []*httpclient.StreamEvent
		aggregatedBody []byte
		aggregatedMeta llm.ResponseMeta
		wantExternalID string
	}{
		{
			name:           "zero client chunks",
			aggregatedBody: []byte(`{}`),
		},
		{
			name: "partial success chunks",
			events: []*httpclient.StreamEvent{{
				Data: []byte(`{"id":"chatcmpl_partial","choices":[{"delta":{"content":"partial"}}]}`),
			}},
			aggregatedBody: []byte(`{"id":"chatcmpl_partial","choices":[{"message":{"content":"partial"}}]}`),
			aggregatedMeta: llm.ResponseMeta{ID: "chatcmpl_partial"},
			wantExternalID: "chatcmpl_partial",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			defer client.Close()

			ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			project := createTestProject(t, ctx, client)
			ch := createTestChannel(t, ctx, client)
			_, requestService, systemService, _ := setupTestServices(t, client)
			require.NoError(t, systemService.SetStoragePolicy(ctx, &biz.StoragePolicy{
				StoreChunks:       false,
				StoreResponseBody: true,
			}))

			req, err := client.Request.Create().
				SetProjectID(project.ID).
				SetChannelID(ch.ID).
				SetModelID("gpt-5").
				SetStatus(request.StatusPending).
				SetRequestBody([]byte(`{"stream":true}`)).
				Save(ctx)
			require.NoError(t, err)

			clientErrorBody := []byte(`{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"context window exceeded"}}`)
			terminalErr := &llm.ResponseError{
				StatusCode: 400,
				Detail: llm.ErrorDetail{
					Type:    "invalid_request_error",
					Code:    "context_length_exceeded",
					Message: "context window exceeded",
				},
			}
			state := &PersistenceState{StreamTerminalError: terminalErr}
			stream := NewInboundPersistentStream(
				ctx,
				&mockStream{events: tt.events},
				req,
				nil,
				requestService,
				&mockInboundTransformer{
					aggregateResponseBody: tt.aggregatedBody,
					aggregateMeta:         tt.aggregatedMeta,
					transformedError:      &httpclient.Error{StatusCode: 400, Body: clientErrorBody},
				},
				nil,
				state,
			)
			for stream.Next() {
				_ = stream.Current()
			}
			require.NoError(t, stream.Close())

			dbReq, err := client.Request.Get(ctx, req.ID)
			require.NoError(t, err)
			require.Equal(t, request.StatusFailed, dbReq.Status)
			require.Equal(t, tt.wantExternalID, dbReq.ExternalID)
			require.JSONEq(t, string(clientErrorBody), string(dbReq.ResponseBody))
			require.Empty(t, dbReq.ResponseChunks)
		})
	}
}

func TestIsTerminalStreamEvent_AudioDoneEvents(t *testing.T) {
	// OpenAI audio SSE streams have no [DONE] sentinel; terminal completion is
	// signaled by typed *.done events surfaced via StreamEvent.Type.
	require.True(t, isTerminalStreamEvent(&httpclient.StreamEvent{Type: "speech.audio.done"}))
	require.True(t, isTerminalStreamEvent(&httpclient.StreamEvent{Type: "transcript.text.done"}))
	require.True(t, isTerminalStreamEvent(&httpclient.StreamEvent{Type: httpclient.BinaryStreamDoneEventType}))

	// Other events must not be treated as terminal.
	require.False(t, isTerminalStreamEvent(&httpclient.StreamEvent{Type: "speech.audio.delta"}))
	require.False(t, isTerminalStreamEvent(&httpclient.StreamEvent{Type: "transcript.text.delta"}))
	require.False(t, isTerminalStreamEvent(&httpclient.StreamEvent{Type: "audio/mpeg"}))
}

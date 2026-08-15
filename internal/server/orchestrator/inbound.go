package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/dumper"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

// ErrStreamIncomplete reports an upstream stream that ended without a terminal
// event and without an aggregated complete response. The same value is persisted
// on the request and delivered to the client, so both sides agree on the reason.
var ErrStreamIncomplete = errors.New("stream ended without terminal event or completed response")

// InboundPersistentStream wraps a stream and tracks all responses for final saving to database.
// It implements the streams.Stream interface and handles persistence in the Close method.
//
//nolint:containedctx // Checked.
type InboundPersistentStream struct {
	ctx            context.Context
	stream         streams.Stream[*httpclient.StreamEvent]
	request        *ent.Request
	requestExec    *ent.RequestExecution
	requestService *biz.RequestService
	transformer    transformer.Inbound
	perf           *biz.PerformanceRecord
	responseChunks []*httpclient.StreamEvent
	closed         bool
	state          *PersistenceState
}

var _ streams.Stream[*httpclient.StreamEvent] = (*InboundPersistentStream)(nil)

func NewInboundPersistentStream(
	ctx context.Context,
	stream streams.Stream[*httpclient.StreamEvent],
	request *ent.Request,
	requestExec *ent.RequestExecution,
	requestService *biz.RequestService,
	transformer transformer.Inbound,
	perf *biz.PerformanceRecord,
	state *PersistenceState,
) *InboundPersistentStream {
	s := &InboundPersistentStream{
		ctx:            ctx,
		stream:         stream,
		request:        request,
		requestExec:    requestExec,
		requestService: requestService,
		transformer:    transformer,
		perf:           perf,
		responseChunks: make([]*httpclient.StreamEvent, 0),
		closed:         false,
		state:          state,
	}

	return s
}

func (ts *InboundPersistentStream) Next() bool {
	return ts.stream.Next()
}

func (ts *InboundPersistentStream) Current() *httpclient.StreamEvent {
	event := ts.stream.Current()
	if event != nil {
		// For raw binary audio chunks (TTS stream_format=audio), persist only a size
		// summary to avoid buffering the full audio payload in memory.
		ts.responseChunks = append(ts.responseChunks, httpclient.SummarizeBinaryChunk(event))
		observeStreamTerminal(ts.state, event)
	}

	return event
}

func observeStreamTerminal(state *PersistenceState, event *httpclient.StreamEvent) {
	if state == nil || event == nil {
		return
	}

	if err := streamTerminalError(event); err != nil {
		state.StreamCompleted = false
		if state.StreamTerminalError == nil {
			state.StreamTerminalError = err
			state.StreamTerminalBody = append([]byte(nil), event.Data...)
		}
		return
	}

	if state.StreamTerminalError == nil && isTerminalStreamEvent(event) {
		state.StreamCompleted = true
	}
}

func streamTerminalError(event *httpclient.StreamEvent) error {
	eventType := event.Type
	root := gjson.ParseBytes(event.Data)
	if eventType == "" {
		eventType = root.Get("type").String()
	}

	switch eventType {
	case "response.failed", "error":
		flatError := false
		candidate := root.Get("response.error")
		if !candidate.Exists() {
			candidate = root.Get("error")
		}
		if !candidate.Exists() && eventType == "error" {
			candidate = root
			flatError = true
		}

		detail := llm.ErrorDetail{
			Code:      candidate.Get("code").String(),
			Message:   candidate.Get("message").String(),
			Type:      candidate.Get("type").String(),
			Param:     candidate.Get("param").String(),
			RequestID: candidate.Get("request_id").String(),
		}
		if detail.Message == "" {
			detail.Message = root.Get("message").String()
		}
		if detail.Code == "" {
			detail.Code = root.Get("code").String()
		}
		if detail.Param == "" {
			detail.Param = root.Get("param").String()
		}
		if detail.RequestID == "" {
			detail.RequestID = root.Get("request_id").String()
		}
		if detail.Type == "" && flatError {
			detail.Type = root.Get("type").String()
		}
		if detail.Type == "" {
			detail.Type = "stream_error"
		}
		if detail.Message == "" {
			detail.Message = "upstream response failed"
		}

		statusCode := event.StatusCode
		if statusCode == 0 {
			statusCode = int(candidate.Get("status_code").Int())
		}
		if statusCode == 0 {
			statusCode = int(root.Get("status").Int())
		}
		if statusCode != 0 && (statusCode < 400 || statusCode > 599) {
			statusCode = 500
		}

		return &llm.ResponseError{StatusCode: statusCode, Detail: detail}
	case "response.incomplete":
		reason := root.Get("response.incomplete_details.reason").String()
		if reason == "" {
			reason = root.Get("incomplete_details.reason").String()
		}
		if reason == "" {
			return errors.New("response incomplete")
		}

		return fmt.Errorf("response incomplete: %s", reason)
	case "response.cancelled":
		return fmt.Errorf("response canceled: %w", context.Canceled)
	default:
		return nil
	}
}

func terminalEventBody(chunks []*httpclient.StreamEvent) []byte {
	for i := len(chunks) - 1; i >= 0; i-- {
		chunk := chunks[i]
		if chunk == nil || len(chunk.Data) == 0 || streamTerminalError(chunk) == nil {
			continue
		}

		return append([]byte(nil), chunk.Data...)
	}

	return nil
}

func terminalResponseID(body []byte) string {
	if id := gjson.GetBytes(body, "response.id").String(); id != "" {
		return id
	}

	return gjson.GetBytes(body, "id").String()
}

// isTerminalStreamEvent checks both SSE metadata and JSON data for a successful
// protocol-level or semantic completion marker.
func isTerminalStreamEvent(event *httpclient.StreamEvent) bool {
	if event == nil {
		return false
	}

	// For chat completions, check for [DONE] event
	if bytes.Equal(event.Data, llm.DoneStreamEvent.Data) ||
		// For Responses API, check for response.completed event
		event.Type == "response.completed" ||
		// For Anthropic Messages API, check for message_stop event
		event.Type == "message_stop" ||
		// For OpenAI audio APIs (TTS sse / STT stream) which have no [DONE] sentinel:
		// rely on the terminal *.done event surfaced as StreamEvent.Type.
		event.Type == "speech.audio.done" ||
		event.Type == "transcript.text.done" ||
		event.Type == httpclient.BinaryStreamDoneEventType {
		return true
	}

	// Compatible SSE providers do not always populate the SSE `event` field and
	// instead carry the event type only in the JSON data. Also recognize a chat
	// completion's finish_reason as semantic completion: clients commonly close
	// the connection immediately after consuming that final useful chunk, before
	// the trailing [DONE] marker is read by the server.
	eventType := gjson.GetBytes(event.Data, "type").String()
	switch eventType {
	case "response.completed", "message_stop", "speech.audio.done", "transcript.text.done":
		return true
	}

	choices := gjson.GetBytes(event.Data, "choices")
	if !choices.IsArray() {
		return false
	}

	completed := false
	choices.ForEach(func(_, choice gjson.Result) bool {
		finishReason := choice.Get("finish_reason")
		completed = finishReason.Type == gjson.String && finishReason.String() != ""

		return !completed
	})

	return completed
}

// IsTerminalStreamEvent reports whether an SSE event successfully completes a stream.
func IsTerminalStreamEvent(event *httpclient.StreamEvent) bool {
	return isTerminalStreamEvent(event)
}

func (ts *InboundPersistentStream) Err() error {
	return ts.stream.Err()
}

func (ts *InboundPersistentStream) Close() error {
	if ts.closed {
		return nil
	}

	ts.closed = true
	ctx := ts.ctx

	log.Debug(ctx, "Closing persistent stream", log.Int("chunk_count", len(ts.responseChunks)), log.Bool("received_done", ts.state.StreamCompleted))

	streamErr := ts.stream.Err()
	ctxErr := ctx.Err()
	if ts.state.StreamTerminalError != nil {
		ts.persistTerminalResponse(ctx, ts.state.StreamTerminalError)
		return ts.stream.Close()
	}

	// If we received the [DONE] event, treat the stream as successfully completed
	// even if there's a context cancellation error. This handles the case where
	// the client disconnects immediately after receiving the last chunk.
	if ts.state.StreamCompleted && (streamErr == nil || errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded)) {
		// Stream completed successfully - perform final persistence
		log.Debug(ctx, "Stream completed successfully (received terminal event), performing final persistence")
		ts.persistResponseChunks(ctx)

		return ts.stream.Close()
	}

	// If there's an explicit stream error (not just context cancellation), treat as failure
	// regardless of what chunks we have. Stream errors indicate the upstream response
	// was incomplete or corrupted.
	if streamErr != nil && !errors.Is(streamErr, context.Canceled) && !errors.Is(streamErr, context.DeadlineExceeded) {
		persistCtx := context.WithoutCancel(ctx)
		ts.persistFailureChunks(persistCtx)

		if ts.request != nil {
			if err := ts.requestService.UpdateRequestStatusFromError(persistCtx, ts.request.ID, streamErr); err != nil {
				log.Warn(persistCtx, "Failed to update request status from error", log.Cause(err))
			}
		}

		return ts.stream.Close()
	}

	// If we haven't received a terminal event, check if the chunks we DO have form a complete response.
	// This handles models that aggregate internally (like Codex) or upstream proxy hung connections
	// where the provider sent the full JSON payload but failed to send [DONE] before dropping.
	var responseBody []byte
	var meta llm.ResponseMeta
	var aggErr error

	if len(ts.responseChunks) > 0 && !ts.state.StreamCompleted {
		responseBody, meta, aggErr = ts.transformer.AggregateStreamChunks(context.WithoutCancel(ctx), ts.responseChunks)
		if aggErr == nil && meta.ID != "" && len(responseBody) > 0 && isCompletedAggregated(meta) {
			log.Debug(ctx, "Stream has valid complete response without terminal event, treating as completed")
			ts.state.StreamCompleted = true
		}
	}

	// Check if context was canceled (client disconnected before [DONE]).
	// Skip the error path if we determined the stream actually completed successfully above.
	if (ctxErr != nil || streamErr != nil) && !ts.state.StreamCompleted {
		persistCtx := context.WithoutCancel(ctx)
		ts.persistFailureChunks(persistCtx)

		if ts.request != nil {
			errToReport := ctxErr
			if errToReport == nil {
				errToReport = streamErr
			}

			if err := ts.requestService.UpdateRequestStatusFromError(persistCtx, ts.request.ID, errToReport); err != nil {
				log.Warn(persistCtx, "Failed to update request status from error", log.Cause(err))
			}
		}

		return ts.stream.Close()
	}

	// If the stream ended without a terminal event and we couldn't determine it was
	// completed through aggregation, mark it as incomplete/failed. This handles the case
	// where the upstream connection drops silently (EOF) without sending a terminal event,
	// which would otherwise fall through and incorrectly mark the request as "completed".
	if !ts.state.StreamCompleted {
		log.Debug(ctx, "Stream ended without terminal event or completed response, treating as incomplete")

		persistCtx := context.WithoutCancel(ctx)
		// Persist partial chunks for debugging; do not mark the request completed.
		ts.persistFailureChunks(persistCtx)

		if ts.request != nil {
			errToReport := ErrStreamIncomplete

			if err := ts.requestService.UpdateRequestStatusFromError(persistCtx, ts.request.ID, errToReport); err != nil {
				log.Warn(persistCtx, "Failed to update request status from error", log.Cause(err))
			}
		}

		return ts.stream.Close()
	}

	// Stream completed successfully - perform final persistence
	log.Debug(ctx, "Stream completed successfully, performing final persistence")

	// We already aggregated the chunks above, so pass them directly to avoid double-aggregation
	if len(responseBody) > 0 {
		ts._persistResponse(context.WithoutCancel(ctx), responseBody, meta)
	} else {
		ts.persistResponseChunks(ctx)
	}

	return ts.stream.Close()
}

func (ts *InboundPersistentStream) persistResponseChunks(ctx context.Context) {
	defer func() {
		if cause := recover(); cause != nil {
			log.Warn(ctx, "Failed to persist inbound response chunks", log.Any("cause", cause))
		}
	}()

	// Use context without cancellation to ensure persistence even if client canceled
	persistCtx := context.WithoutCancel(ctx)

	// Aggregate stream chunks first, then delegate to _persistResponse
	responseBody, meta, err := ts.transformer.AggregateStreamChunks(persistCtx, ts.responseChunks)
	if err != nil {
		log.Warn(persistCtx, "Failed to aggregate chunks for main request", log.Cause(err))
		dumper.DumpStreamEvents(persistCtx, ts.responseChunks, "response_chunks.json")
	}

	ts._persistResponse(persistCtx, responseBody, meta)
}

func (ts *InboundPersistentStream) persistTerminalResponse(ctx context.Context, terminalErr error) {
	if ts.request == nil {
		return
	}

	persistCtx := context.WithoutCancel(ctx)
	terminalBody := terminalEventBody(ts.responseChunks)
	responseBody := terminalBody
	externalID := terminalResponseID(terminalBody)
	if len(terminalBody) > 0 {
		if aggregatedBody, meta, aggErr := ts.transformer.AggregateStreamChunks(persistCtx, ts.responseChunks); aggErr == nil {
			responseBody = aggregatedBody
			if externalID == "" {
				externalID = meta.ID
			}
		}
	} else {
		if _, meta, aggErr := ts.transformer.AggregateStreamChunks(persistCtx, ts.responseChunks); aggErr == nil {
			externalID = meta.ID
		}
		if transformed := ts.transformer.TransformError(persistCtx, terminalErr); transformed != nil {
			responseBody = transformed.Body
		}
	}

	status := request.StatusFailed
	if errors.Is(terminalErr, context.Canceled) {
		status = request.StatusCanceled
	}
	if len(responseBody) > 0 {
		if err := ts.requestService.UpdateRequestStatusExternalIDAndResponseBody(
			persistCtx,
			ts.request.ID,
			status,
			externalID,
			responseBody,
			latencyMetrics(ts.perf),
		); err != nil {
			log.Warn(persistCtx, "Failed to persist terminal request response", log.Cause(err))
		}
	} else if err := ts.requestService.UpdateRequestStatusFromError(persistCtx, ts.request.ID, terminalErr); err != nil {
		log.Warn(persistCtx, "Failed to update terminal request status", log.Cause(err))
	}

	if err := ts.requestService.SaveRequestChunks(persistCtx, ts.request.ID, ts.responseChunks); err != nil {
		log.Warn(persistCtx, "Failed to save terminal request chunks", log.Cause(err))
	}
}

// persistFailureChunks stores buffered SSE chunks for a failed/incomplete stream
// without marking the request completed. Used so truncated upstream responses remain
// inspectable when store_chunks is enabled.
func (ts *InboundPersistentStream) persistFailureChunks(ctx context.Context) {
	if ts.request == nil || len(ts.responseChunks) == 0 {
		return
	}

	if err := ts.requestService.SaveRequestChunks(ctx, ts.request.ID, ts.responseChunks); err != nil {
		log.Warn(ctx, "Failed to save request chunks after stream failure", log.Cause(err))
	}
}

// _persistResponse performs the actual persistence with pre-aggregated data.
// This avoids redundant aggregation when the data is already available.
func (ts *InboundPersistentStream) _persistResponse(ctx context.Context, responseBody []byte, meta llm.ResponseMeta) {
	if ts.request == nil {
		return
	}

	err := ts.requestService.UpdateRequestCompleted(ctx, ts.request.ID, meta.ID, responseBody, latencyMetrics(ts.perf))
	if err != nil {
		log.Warn(ctx, "Failed to update request status to completed", log.Cause(err))
	}

	// Save all response chunks at once
	if err := ts.requestService.SaveRequestChunks(ctx, ts.request.ID, ts.responseChunks); err != nil {
		log.Warn(ctx, "Failed to save request chunks", log.Cause(err))
	}
}

func latencyMetrics(perf *biz.PerformanceRecord) *biz.LatencyMetrics {
	if perf == nil {
		return nil
	}

	firstTokenLatencyMs, requestLatencyMs, _ := perf.Calculate()
	metrics := &biz.LatencyMetrics{LatencyMs: &requestLatencyMs}
	if perf.Stream && perf.FirstTokenTime != nil {
		metrics.FirstTokenLatencyMs = &firstTokenLatencyMs
	}

	return metrics
}

// PersistentInboundTransformer wraps an inbound transformer with enhanced capabilities.
type PersistentInboundTransformer struct {
	wrapped transformer.Inbound
	state   *PersistenceState
}

func (p *PersistentInboundTransformer) TransformError(ctx context.Context, rawErr error) *httpclient.Error {
	return p.wrapped.TransformError(ctx, rawErr)
}

func (p *PersistentInboundTransformer) TransformRequest(ctx context.Context, request *httpclient.Request) (*llm.Request, error) {
	llmRequest, err := p.wrapped.TransformRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	llmRequest.RawRequest = request
	p.state.RawRequest = request
	p.state.LlmRequest = llmRequest
	p.state.OriginalRequestStream = llmRequest.Stream

	return llmRequest, nil
}

func (p *PersistentInboundTransformer) TransformResponse(ctx context.Context, response *llm.Response) (*httpclient.Response, error) {
	return p.wrapped.TransformResponse(ctx, response)
}

func (p *PersistentInboundTransformer) TransformStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	channelStream, err := p.wrapped.TransformStream(ctx, stream)
	if err != nil {
		return nil, err
	}

	persistentStream := NewInboundPersistentStream(
		ctx,
		channelStream,
		p.state.Request,
		p.state.RequestExec,
		p.state.RequestService,
		p, // Use the PersistentInboundTransformer as the transformer
		p.state.Perf,
		p.state,
	)

	return persistentStream, nil
}

func (p *PersistentInboundTransformer) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return p.wrapped.AggregateStreamChunks(ctx, chunks)
}

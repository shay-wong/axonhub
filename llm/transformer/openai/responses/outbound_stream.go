package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/internal/pkg/xurl"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

// ErrStreamIncomplete is returned when the stream ends without a terminal event
// (response.completed, response.failed, response.cancelled, or response.incomplete).
var ErrStreamIncomplete = errors.New("stream ended without terminal event")

// TransformStream transforms OpenAI Responses API SSE events to unified llm.Response stream.
func (t *OutboundTransformer) TransformStream(
	ctx context.Context,
	req *httpclient.Request,
	stream streams.Stream[*httpclient.StreamEvent],
) (streams.Stream[*llm.Response], error) {
	// Append the DONE event to the stream
	doneEvent := lo.ToPtr(llm.DoneStreamEvent)
	streamWithDone := streams.AppendStream(stream, doneEvent)

	return streams.NoNil(newResponsesOutboundStream(streamWithDone, req)), nil
}

// responsesOutboundStream wraps a stream and maintains state during processing.
type responsesOutboundStream struct {
	stream streams.Stream[*httpclient.StreamEvent]
	state  *outboundStreamState

	// Event queue
	eventQueue []*llm.Response
	queueIndex int
	err        error

	// Track whether the response completed successfully
	responseCompleted bool
}

// outboundStreamState holds the state for a streaming session.
type outboundStreamState struct {
	responseID         string
	responseModel      string
	previousResponseID *string
	serviceTier        string
	usage              *llm.Usage
	created            int64

	// Content accumulation
	textContent      strings.Builder
	refusalContent   strings.Builder
	reasoningContent strings.Builder

	// Tool call tracking
	toolCalls     map[string]*llm.ToolCall // callID -> tool call
	itemToCallID  map[string]string        // item.id -> call_id mapping
	toolCallIndex map[string]int           // callID -> index in the output
	serverItems   map[string]bool          // server-managed item key -> emitted

	// Reasoning signature tracking
	pendingReasoningEncryptedContent map[string]*string

	// Transformer metadata tracking
	transformerMetadata        map[string]any
	transformerMetadataEmitted bool
}

func newResponsesOutboundStream(stream streams.Stream[*httpclient.StreamEvent], req *httpclient.Request) *responsesOutboundStream {
	transformerMetadata := map[string]any{}
	if req != nil && req.TransformerMetadata != nil {
		for key, value := range req.TransformerMetadata {
			transformerMetadata[key] = value
		}
	}

	return &responsesOutboundStream{
		stream: stream,
		state: &outboundStreamState{
			toolCalls:                        make(map[string]*llm.ToolCall),
			itemToCallID:                     make(map[string]string),
			toolCallIndex:                    make(map[string]int),
			serverItems:                      make(map[string]bool),
			pendingReasoningEncryptedContent: make(map[string]*string),
			transformerMetadata:              transformerMetadata,
		},
	}
}

func newStreamResponseError(statusCode int, upstreamError *Error) *llm.ResponseError {
	if statusCode < http.StatusBadRequest || statusCode > 599 {
		statusCode = http.StatusInternalServerError
	}
	detail := llm.ErrorDetail{
		Code:      upstreamError.Code,
		Message:   upstreamError.Message,
		Type:      upstreamError.Type,
		Param:     lo.FromPtr(upstreamError.Param),
		RequestID: upstreamError.RequestID,
	}
	if detail.Message == "" {
		detail.Message = "upstream request failed"
	}

	return &llm.ResponseError{StatusCode: statusCode, Detail: detail}
}

func (s *responsesOutboundStream) enqueue(resp *llm.Response) {
	s.eventQueue = append(s.eventQueue, resp)
}

func (s *responsesOutboundStream) applyResponseSnapshot(resp *llm.Response, snapshot *Response) {
	if snapshot != nil {
		if snapshot.ID != "" {
			s.state.responseID = snapshot.ID
		}
		if snapshot.Model != "" {
			s.state.responseModel = snapshot.Model
		}
		if snapshot.CreatedAt != 0 {
			s.state.created = snapshot.CreatedAt
		}
		if snapshot.PreviousResponseID != nil {
			s.state.previousResponseID = snapshot.PreviousResponseID
		}
		if snapshot.ServiceTier != nil {
			s.state.serviceTier = *snapshot.ServiceTier
		}
		if snapshot.Usage != nil {
			s.state.usage = snapshot.Usage.ToUsage()
		}
	}

	resp.ID = s.state.responseID
	resp.Model = s.state.responseModel
	resp.Created = s.state.created
	resp.PreviousResponseID = s.state.previousResponseID
	resp.ServiceTier = s.state.serviceTier
	resp.Usage = s.state.usage
}

func (s *responsesOutboundStream) applyRefusal(resp *llm.Response, refusal string, final bool) bool {
	if refusal == "" {
		return false
	}

	delta := refusal
	if final {
		existing := s.state.refusalContent.String()
		if !strings.HasPrefix(refusal, existing) {
			return false
		}
		delta = strings.TrimPrefix(refusal, existing)
	}
	if delta == "" {
		return false
	}

	s.state.refusalContent.WriteString(delta)
	resp.Choices = []llm.Choice{{
		Index: 0,
		Delta: &llm.Message{Refusal: delta},
	}}

	return true
}

func (s *responsesOutboundStream) enqueueTerminalRefusal(resp *llm.Response, snapshot *Response) {
	if snapshot == nil || len(snapshot.Output) == 0 {
		return
	}

	msg := convertOutputToMessage(snapshot.Output, s.state.transformerMetadata)
	refusalResp := *resp
	if s.applyRefusal(&refusalResp, msg.Refusal, true) {
		s.enqueue(&refusalResp)
	}
}

func (s *responsesOutboundStream) setServerItemContent(resp *llm.Response, item *Item) (bool, error) {
	key := serverItemKey(item)
	if key != "" {
		if s.state.serverItems[key] {
			return false, nil
		}
		s.state.serverItems[key] = true
	}

	rawItem, err := json.Marshal(item)
	if err != nil {
		return false, fmt.Errorf("failed to marshal %s item: %w", item.Type, err)
	}

	resp.Choices = []llm.Choice{
		{
			Index: 0,
			Delta: &llm.Message{
				Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{
						{
							Type:        item.Type,
							ServerBlock: rawItem,
						},
					},
				},
			},
		},
	}

	return true, nil
}

func (s *responsesOutboundStream) setToolSearchBridgeContent(resp *llm.Response, item *Item) (bool, error) {
	if anthropicFunctionToolSearchName(s.state.transformerMetadata) == "" {
		return false, nil
	}

	key := serverItemKey(item)
	if key != "" {
		if s.state.serverItems[key] {
			return false, nil
		}
		s.state.serverItems[key] = true
	}

	msg, err := convertOutputToMessageWithError([]Item{*item}, s.state.transformerMetadata)
	if err != nil {
		return false, err
	}

	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			toolCallIdx, ok := s.state.toolCallIndex[tc.ID]
			if !ok {
				toolCallIdx = len(s.state.toolCalls)
				s.state.toolCallIndex[tc.ID] = toolCallIdx
			}

			toolCall := tc
			toolCall.Index = toolCallIdx
			s.state.toolCalls[tc.ID] = &toolCall
			if item.ID != "" {
				s.state.itemToCallID[item.ID] = tc.ID
			}

			resp.Choices = []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{
						ToolCalls: []llm.ToolCall{toolCall},
					},
				},
			}
			return true, nil
		}
	}

	if len(msg.InlineToolResults) > 0 {
		resp.Choices = []llm.Choice{
			{
				Index: 0,
				Delta: &llm.Message{
					InlineToolResults: msg.InlineToolResults,
				},
			},
		}
		return true, nil
	}

	return false, nil
}

func shouldEmitServerItemFromAdded(item *Item) bool {
	if item.Type != "tool_search_call" {
		return true
	}

	return hasToolSearchArguments(item)
}

func shouldEmitServerItemFromDone(item *Item) bool {
	if item.Type != "tool_search_call" {
		return true
	}

	return hasToolSearchArguments(item)
}

func hasToolSearchArguments(item *Item) bool {
	args := strings.TrimSpace(item.Arguments)
	return args != "" && args != "{}" && args != "null"
}

func serverItemKey(item *Item) string {
	if item.ID != "" {
		return item.Type + ":" + item.ID
	}
	if item.CallID != "" {
		return item.Type + ":" + item.CallID
	}

	return ""
}

func (s *responsesOutboundStream) Next() bool {
	// If we have events in the queue, return them first
	if s.queueIndex < len(s.eventQueue) {
		return true
	}

	// Clear the queue and reset index for new events
	s.eventQueue = nil
	s.queueIndex = 0

	// Try to get the next chunk from source
	if !s.stream.Next() {
		// Stream ended - check if we received a terminal event
		// If not, this is an incomplete stream (e.g., upstream EOF)
		if s.err == nil && !s.responseCompleted && s.stream.Err() == nil {
			// Only set this error if we had started receiving response data
			// This distinguishes between "no response" and "incomplete response"
			if s.state.responseID != "" {
				s.err = ErrStreamIncomplete
			}
		}
		return false
	}

	event := s.stream.Current()

	err := s.transformStreamChunk(event)
	if err != nil {
		s.err = err
		return false
	}

	// Continue to the next event if no events were enqueued
	return s.Next()
}

// transformStreamChunk transforms a single OpenAI Responses API streaming chunk to unified llm.Response.
// Events are enqueued via s.enqueue() instead of being returned.
//
//nolint:maintidx,gocognit // It is complex and hard to split.
func (s *responsesOutboundStream) transformStreamChunk(event *httpclient.StreamEvent) error {
	if event == nil || len(event.Data) == 0 {
		return nil
	}

	// Handle [DONE] marker
	if string(event.Data) == "[DONE]" {
		s.enqueue(llm.DoneResponse)
		return nil
	}

	// Parse the streaming event
	var streamEvent StreamEvent

	err := json.Unmarshal(event.Data, &streamEvent)
	if err != nil {
		return fmt.Errorf("failed to unmarshal responses api stream event: %w", err)
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		slog.DebugContext(context.Background(), "received response stream event", slog.Any("event", streamEvent))
	}

	if streamEvent.Response != nil && streamEvent.Response.ServiceTier != nil {
		s.state.serviceTier = *streamEvent.Response.ServiceTier
	}

	// Build base response
	resp := &llm.Response{
		Object:             "chat.completion.chunk",
		ID:                 s.state.responseID,
		Model:              s.state.responseModel,
		Created:            s.state.created,
		PreviousResponseID: s.state.previousResponseID,
		ServiceTier:        s.state.serviceTier,
	}

	//nolint:exhaustive //Only process events we care about.
	switch streamEvent.Type {
	case StreamEventTypeResponseCreated:
		if streamEvent.Response != nil {
			s.state.responseID = streamEvent.Response.ID
			s.state.responseModel = streamEvent.Response.Model
			s.state.created = streamEvent.Response.CreatedAt
			s.state.previousResponseID = streamEvent.Response.PreviousResponseID

			resp.ID = s.state.responseID
			resp.Model = s.state.responseModel
			resp.Created = s.state.created
			resp.PreviousResponseID = s.state.previousResponseID

			if streamEvent.Response.Usage != nil {
				s.state.usage = streamEvent.Response.Usage.ToUsage()
				resp.Usage = s.state.usage
			}
		}

		resp.Choices = []llm.Choice{
			{
				Index: 0,
				Delta: &llm.Message{
					Role: "assistant",
				},
			},
		}

	case StreamEventTypeResponseInProgress:
		// Update state but don't emit an event
		if streamEvent.Response != nil {
			s.state.responseID = streamEvent.Response.ID
			s.state.responseModel = streamEvent.Response.Model
			s.state.created = streamEvent.Response.CreatedAt
			s.state.previousResponseID = streamEvent.Response.PreviousResponseID

			if streamEvent.Response.Usage != nil {
				s.state.usage = streamEvent.Response.Usage.ToUsage()
			}
		}

		return nil // Intentionally skip this event
	case StreamEventTypeOutputItemAdded:
		// Output item added - check type to determine how to handle
		if streamEvent.Item == nil {
			// No item data, skip
			return nil // Intentionally skip this event
		}

		item := streamEvent.Item
		switch item.Type {
		case "reasoning":
			if item.ID == "" || item.EncryptedContent == nil || *item.EncryptedContent == "" {
				return nil // Intentionally skip this event
			}

			// Responses streams may send a provisional encrypted_content on item.added
			// and the final blob on item.done. Hold the value until item.done so the
			// final blob replaces the provisional one instead of being concatenated.
			s.state.pendingReasoningEncryptedContent[item.ID] = shared.EncodeOpenAIEncryptedContent(item.EncryptedContent)
			return nil

		case "function_call":
			// Initialize tool call tracking
			toolCallIdx := len(s.state.toolCalls)
			s.state.toolCalls[item.CallID] = &llm.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: llm.FunctionCall{
					Name:      item.Name,
					Namespace: item.Namespace,
					Arguments: "",
				},
			}
			// Map item.id to call_id for later lookup
			s.state.itemToCallID[item.ID] = item.CallID
			s.state.toolCallIndex[item.CallID] = toolCallIdx

			resp.Choices = []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{
						ToolCalls: []llm.ToolCall{
							{
								ID:    item.CallID,
								Type:  "function",
								Index: toolCallIdx,
								Function: llm.FunctionCall{
									Name:      item.Name,
									Namespace: item.Namespace,
								},
							},
						},
					},
				},
			}

		case "custom_tool_call":
			// Custom tool call - initialize tracking, input will be streamed via delta events
			toolCallIdx := len(s.state.toolCalls)
			s.state.toolCalls[item.CallID] = &llm.ToolCall{
				ID:   item.CallID,
				Type: llm.ToolTypeResponsesCustomTool,
				ResponseCustomToolCall: &llm.ResponseCustomToolCall{
					CallID: item.CallID,
					Name:   item.Name,
					Input:  "",
				},
			}
			s.state.itemToCallID[item.ID] = item.CallID
			s.state.toolCallIndex[item.CallID] = toolCallIdx

			resp.Choices = []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{
						ToolCalls: []llm.ToolCall{
							{
								ID:    item.CallID,
								Type:  llm.ToolTypeResponsesCustomTool,
								Index: toolCallIdx,
								ResponseCustomToolCall: &llm.ResponseCustomToolCall{
									CallID: item.CallID,
									Name:   item.Name,
								},
							},
						},
					},
				},
			}

		case "tool_search_call", "tool_search_output":
			if !shouldEmitServerItemFromAdded(item) {
				return nil
			}

			emitted, err := s.setToolSearchBridgeContent(resp, item)
			if err != nil {
				return err
			}
			if emitted {
				break
			}

			emitted, err = s.setServerItemContent(resp, item)
			if err != nil {
				return err
			}
			if !emitted {
				return nil
			}

		default:
			// For other item types (e.g., message), skip - no meaningful content to emit
			return nil // Intentionally skip this event
		}

	case StreamEventTypeFunctionCallArgumentsDelta:
		// Function call arguments delta
		if streamEvent.ItemID != nil {
			// Look up call_id from item_id mapping
			callID, ok := s.state.itemToCallID[*streamEvent.ItemID]
			if !ok {
				// Fallback: item_id might be the call_id itself
				callID = *streamEvent.ItemID
			}

			if tc, ok := s.state.toolCalls[callID]; ok {
				tc.Function.Arguments += streamEvent.Delta
				toolCallIdx := s.state.toolCallIndex[callID]

				resp.Choices = []llm.Choice{
					{
						Index: 0,
						Delta: &llm.Message{
							ToolCalls: []llm.ToolCall{
								{
									Index: toolCallIdx,
									Function: llm.FunctionCall{
										Arguments: streamEvent.Delta,
									},
								},
							},
						},
					},
				}
			}
		}

	case StreamEventTypeFunctionCallArgumentsDone:
		callID := streamEvent.CallID
		if callID == "" && streamEvent.ItemID != nil {
			var ok bool
			callID, ok = s.state.itemToCallID[*streamEvent.ItemID]
			if !ok {
				callID = *streamEvent.ItemID
			}
		}

		tc, ok := s.state.toolCalls[callID]
		if !ok {
			return nil
		}

		if streamEvent.Name != "" {
			tc.Function.Name = streamEvent.Name
		}
		if streamEvent.Namespace != "" {
			tc.Function.Namespace = streamEvent.Namespace
		}

		previousArguments := tc.Function.Arguments
		tc.Function.Arguments = streamEvent.Arguments

		argumentDelta := ""
		if strings.HasPrefix(streamEvent.Arguments, previousArguments) {
			argumentDelta = strings.TrimPrefix(streamEvent.Arguments, previousArguments)
		}
		if argumentDelta == "" {
			return nil
		}

		toolCallIdx := s.state.toolCallIndex[callID]
		resp.Choices = []llm.Choice{
			{
				Index: 0,
				Delta: &llm.Message{
					ToolCalls: []llm.ToolCall{
						{
							Index: toolCallIdx,
							Function: llm.FunctionCall{
								Arguments: argumentDelta,
							},
						},
					},
				},
			},
		}

	case StreamEventTypeCustomToolCallInputDelta:
		// Custom tool call input delta - accumulate and emit as tool call delta
		if streamEvent.ItemID != nil {
			callID, ok := s.state.itemToCallID[*streamEvent.ItemID]
			if !ok {
				callID = *streamEvent.ItemID
			}

			if tc, ok := s.state.toolCalls[callID]; ok {
				tc.ResponseCustomToolCall.Input += streamEvent.Delta
				toolCallIdx := s.state.toolCallIndex[callID]

				resp.Choices = []llm.Choice{
					{
						Index: 0,
						Delta: &llm.Message{
							ToolCalls: []llm.ToolCall{
								{
									Index: toolCallIdx,
									Type:  llm.ToolTypeResponsesCustomTool,
									ResponseCustomToolCall: &llm.ResponseCustomToolCall{
										CallID: callID,
										Name:   tc.ResponseCustomToolCall.Name,
										Input:  streamEvent.Delta,
									},
								},
							},
						},
					},
				}
			}
		}

	case StreamEventTypeCustomToolCallInputDone:
		// Custom tool call input completed - update state but don't emit an event
		if streamEvent.ItemID != nil {
			callID, ok := s.state.itemToCallID[*streamEvent.ItemID]
			if !ok {
				callID = *streamEvent.ItemID
			}

			if tc, ok := s.state.toolCalls[callID]; ok {
				tc.ResponseCustomToolCall.Input = streamEvent.Input
			}
		}

		return nil // Intentionally skip this event

	case StreamEventTypeContentPartAdded:
		if streamEvent.Part != nil && streamEvent.Part.Type == "refusal" && streamEvent.Part.Refusal != nil &&
			s.applyRefusal(resp, *streamEvent.Part.Refusal, true) {
			break
		}

		// Other content parts carry no new content.
		return nil // Intentionally skip this event

	case StreamEventTypeOutputTextDelta:
		// Text content delta
		s.state.textContent.WriteString(streamEvent.Delta)

		resp.Choices = []llm.Choice{
			{
				Index: 0,
				Delta: &llm.Message{
					Content: llm.MessageContent{
						Content: &streamEvent.Delta,
					},
				},
			},
		}

	case StreamEventTypeReasoningSummaryTextDelta, StreamEventTypeReasoningTextDelta:
		// Reasoning content delta
		s.state.reasoningContent.WriteString(streamEvent.Delta)
		itemID := lo.FromPtr(streamEvent.ItemID)
		if itemID == "" {
			return nil // Intentionally skip an unassociated reasoning delta
		}
		resp.TransformerMetadata = map[string]any{
			responsesReasoningItemTransformerMetadataKey: map[string]any{
				"id": itemID,
			},
		}

		resp.Choices = []llm.Choice{
			{
				Index: 0,
				Delta: &llm.Message{
					ID:               itemID,
					ReasoningContent: &streamEvent.Delta,
				},
			},
		}

	case StreamEventTypeOutputTextDone:
		// Text content completed - skip, content was already streamed via deltas
		return nil // Intentionally skip this event

	case StreamEventTypeRefusalDelta:
		if !s.applyRefusal(resp, streamEvent.Delta, false) {
			return nil
		}

	case StreamEventTypeRefusalDone:
		if !s.applyRefusal(resp, streamEvent.Refusal, true) {
			return nil
		}

	case StreamEventTypeReasoningSummaryTextDone, StreamEventTypeReasoningTextDone:
		// Reasoning content completed - skip, content was already streamed via deltas
		return nil // Intentionally skip this event

	case StreamEventTypeOutputItemDone:
		if streamEvent.Item == nil {
			return nil // Intentionally skip this event
		}
		if streamEvent.Item.Type == "compaction" || streamEvent.Item.Type == "compaction_summary" {
			resp.Choices = []llm.Choice{{
				Index: 0,
				Delta: lo.ToPtr(convertOutputToMessage([]Item{*streamEvent.Item}, s.state.transformerMetadata)),
			}}
			break
		}
		if streamEvent.Item.Type == "web_search_call" {
			appendResponseWebSearchCallMetadata(s.state.transformerMetadata, *streamEvent.Item)
			return nil // Intentionally skip this event
		}
		if streamEvent.Item.Type == "tool_search_call" || streamEvent.Item.Type == "tool_search_output" {
			if !shouldEmitServerItemFromDone(streamEvent.Item) {
				return nil
			}

			emitted, err := s.setToolSearchBridgeContent(resp, streamEvent.Item)
			if err != nil {
				return err
			}
			if emitted {
				break
			}

			emitted, err = s.setServerItemContent(resp, streamEvent.Item)
			if err != nil {
				return err
			}
			if !emitted {
				return nil
			}

			break
		}
		if streamEvent.Item.Type == "reasoning" {
			if streamEvent.Item.ID == "" {
				return nil // Intentionally skip this event
			}

			encryptedContent := shared.EncodeOpenAIEncryptedContent(streamEvent.Item.EncryptedContent)
			if encryptedContent == nil || *encryptedContent == "" {
				encryptedContent = s.state.pendingReasoningEncryptedContent[streamEvent.Item.ID]
			}
			delete(s.state.pendingReasoningEncryptedContent, streamEvent.Item.ID)
			if encryptedContent == nil || *encryptedContent == "" {
				return nil // Intentionally skip this event
			}

			resp.TransformerMetadata = map[string]any{
				responsesReasoningItemTransformerMetadataKey: map[string]any{
					"id":   streamEvent.Item.ID,
					"done": true,
				},
			}
			resp.Choices = []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{
						ReasoningSignature: encryptedContent,
					},
				},
			}

			break
		}
		if streamEvent.Item.Type != "message" {
			return nil // Intentionally skip this event
		}

		msg, err := convertOutputToMessageWithError([]Item{*streamEvent.Item}, s.state.transformerMetadata)
		if err != nil {
			return err
		}
		refusalDelta := ""
		if strings.HasPrefix(msg.Refusal, s.state.refusalContent.String()) {
			refusalDelta = strings.TrimPrefix(msg.Refusal, s.state.refusalContent.String())
		}
		if len(msg.Annotations) == 0 && refusalDelta == "" {
			return nil // Intentionally skip this event
		}
		s.state.refusalContent.WriteString(refusalDelta)
		if len(s.state.transformerMetadata) > 0 {
			resp.TransformerMetadata = s.state.transformerMetadata
			s.state.transformerMetadataEmitted = true
		}

		resp.Choices = []llm.Choice{
			{
				Index: 0,
				Delta: &llm.Message{
					Annotations: msg.Annotations,
					Refusal:     refusalDelta,
				},
			},
		}

	case StreamEventTypeContentPartDone:
		if streamEvent.Part != nil && streamEvent.Part.Type == "refusal" && streamEvent.Part.Refusal != nil &&
			s.applyRefusal(resp, *streamEvent.Part.Refusal, true) {
			break
		}

		return nil

	case StreamEventTypeReasoningSummaryPartAdded, StreamEventTypeReasoningSummaryPartDone:
		// These events don't need special handling - skip
		return nil // Intentionally skip this event

	case StreamEventTypeResponseCompleted:
		// Response completed - emit two events: one with finish_reason, one with usage
		s.responseCompleted = true
		s.applyResponseSnapshot(resp, streamEvent.Response)
		resp.Usage = nil
		s.enqueueTerminalRefusal(resp, streamEvent.Response)
		if len(s.state.transformerMetadata) > 0 && !s.state.transformerMetadataEmitted {
			resp.TransformerMetadata = s.state.transformerMetadata
			s.state.transformerMetadataEmitted = true
		}

		finishReason := "stop"
		if len(s.state.toolCalls) > 0 {
			finishReason = "tool_calls"
		}

		// First event: finish_reason with empty delta
		resp.Choices = []llm.Choice{
			{
				Index:        0,
				Delta:        &llm.Message{},
				FinishReason: &finishReason,
			},
		}

		// Second event: usage (if available)
		if streamEvent.Response != nil && streamEvent.Response.Usage != nil {
			s.state.usage = streamEvent.Response.Usage.ToUsage()
			usageResp := &llm.Response{
				Object:             "chat.completion.chunk",
				ID:                 s.state.responseID,
				Model:              s.state.responseModel,
				Created:            s.state.created,
				PreviousResponseID: s.state.previousResponseID,
				ServiceTier:        s.state.serviceTier,
				Choices:            []llm.Choice{},
				Usage:              s.state.usage,
			}

			s.enqueue(resp)
			s.enqueue(usageResp)

			return nil
		}

	case StreamEventTypeResponseFailed:
		// Response failed
		s.responseCompleted = true
		upstreamError := streamEvent.Error
		s.applyResponseSnapshot(resp, streamEvent.Response)
		s.enqueueTerminalRefusal(resp, streamEvent.Response)
		if streamEvent.Response != nil {
			if streamEvent.Response.Error != nil {
				upstreamError = streamEvent.Response.Error
			}
		}
		if upstreamError == nil {
			upstreamError = &Error{
				Type:    "server_error",
				Code:    "response_failed",
				Message: "upstream response failed",
			}
		}
		resp.Error = newStreamResponseError(streamEvent.StatusCode, upstreamError)
		resp.ProviderTerminalOutcome = llm.ResponseTerminalOutcomeFailed
		finishReason := "error"
		resp.Choices = []llm.Choice{
			{
				Index:        0,
				FinishReason: &finishReason,
			},
		}

	case StreamEventTypeResponseIncomplete:
		// Response incomplete (e.g., max tokens)
		s.responseCompleted = true
		s.applyResponseSnapshot(resp, streamEvent.Response)
		s.enqueueTerminalRefusal(resp, streamEvent.Response)
		resp.ProviderTerminalOutcome = llm.ResponseTerminalOutcomeIncomplete
		finishReason := "length"
		if streamEvent.Response != nil && streamEvent.Response.IncompleteDetails != nil &&
			streamEvent.Response.IncompleteDetails.Reason == "content_filter" {
			finishReason = "content_filter"
		}
		resp.Choices = []llm.Choice{
			{
				Index:        0,
				FinishReason: &finishReason,
			},
		}

	case StreamEventTypeResponseCancelled:
		// Response cancelled
		s.responseCompleted = true
		s.applyResponseSnapshot(resp, streamEvent.Response)
		s.enqueueTerminalRefusal(resp, streamEvent.Response)
		resp.ProviderTerminalOutcome = llm.ResponseTerminalOutcomeCanceled
		finishReason := "cancelled"
		resp.Choices = []llm.Choice{
			{
				Index:        0,
				FinishReason: &finishReason,
			},
		}

	case StreamEventTypeError:
		upstreamError := streamEvent.Error
		if upstreamError == nil {
			upstreamError = &Error{
				Type:      string(streamEvent.Type),
				Code:      streamEvent.Code,
				Message:   streamEvent.Message,
				Param:     streamEvent.Param,
				RequestID: streamEvent.RequestID,
			}
		}
		return newStreamResponseError(streamEvent.StatusCode, upstreamError)

	case StreamEventTypeImageGenerationPartialImage,
		StreamEventTypeImageGenerationGenerating,
		StreamEventTypeImageGenerationInProgress,
		StreamEventTypeImageGenerationCompleted:
		// Handle image generation events
		if streamEvent.PartialImageB64 != "" {
			imageURL := xurl.BuildDataURL("image/png", streamEvent.PartialImageB64, true)
			resp.Choices = []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{
						Content: llm.MessageContent{
							MultipleContent: []llm.MessageContentPart{
								{
									Type: "image_url",
									ImageURL: &llm.ImageURL{
										URL: imageURL,
									},
								},
							},
						},
					},
				},
			}
		} else {
			resp.Choices = []llm.Choice{
				{
					Index: 0,
					Delta: &llm.Message{},
				},
			}
		}

	default:
		// Unknown event type - skip
		return nil // Intentionally skip this event
	}

	s.enqueue(resp)

	return nil
}

func (s *responsesOutboundStream) Current() *llm.Response {
	if s.queueIndex < len(s.eventQueue) {
		event := s.eventQueue[s.queueIndex]
		s.queueIndex++

		return event
	}

	return nil
}

func (s *responsesOutboundStream) Err() error {
	if s.err != nil {
		return s.err
	}

	return s.stream.Err()
}

func (s *responsesOutboundStream) Close() error {
	return s.stream.Close()
}

// AggregateStreamChunks aggregates OpenAI Responses API streaming chunks into a complete response.
func (t *OutboundTransformer) AggregateStreamChunks(
	ctx context.Context, _ *httpclient.Request,
	chunks []*httpclient.StreamEvent,
) ([]byte, llm.ResponseMeta, error) {
	return AggregateStreamChunks(ctx, chunks)
}

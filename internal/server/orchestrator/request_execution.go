package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/pkg/xcontext"
	"github.com/looplj/axonhub/internal/pkg/xerrors"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

// Precompiled regex patterns for sanitizeResponseBody to avoid recompiling on each call.
var (
	tokenRegex        = regexp.MustCompile(`(?i)(bearer[\s:=]+)[a-zA-Z0-9_\-\.]+`)
	apiKeyRegex       = regexp.MustCompile(`(?i)(api[_-]?key["']?\s*[:=]\s*["']?)([a-zA-Z0-9_\-.]{8,})(["']?)`)
	secretKeyRegex    = regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{8,}\b`)
	emailRegex        = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	imageDataURLRegex = regexp.MustCompile(`(?i)data:image/[a-z0-9.+-]+;base64,[a-zA-Z0-9+/_=-]+`)
	longBase64Regex   = regexp.MustCompile(`[a-zA-Z0-9+/_-]{128,}={0,2}`)
)

type responsesExecutionDiagnostic struct {
	Status           string                    `json:"status"`
	IncompleteReason string                    `json:"incomplete_reason,omitempty"`
	OutputTypes      []string                  `json:"output_types,omitempty"`
	RefusalSummary   string                    `json:"refusal_summary,omitempty"`
	MessageSummary   string                    `json:"message_summary,omitempty"`
	Error            *responsesDiagnosticError `json:"error,omitempty"`
}

type responsesDiagnosticError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// sanitizeResponseBody redacts obvious secrets and truncates the body for safe logging.
func sanitizeResponseBody(body []byte, maxLen int) []byte {
	if len(body) == 0 {
		return body
	}

	str := string(body)

	// Redact bearer tokens (case-insensitive), preserving the bearer prefix
	str = tokenRegex.ReplaceAllString(str, "${1}[REDACTED]")

	// Redact API keys (common patterns)
	str = apiKeyRegex.ReplaceAllString(str, "${1}[REDACTED]${3}")
	str = secretKeyRegex.ReplaceAllString(str, "[API KEY REDACTED]")

	// Redact email addresses
	str = emailRegex.ReplaceAllString(str, "[EMAIL REDACTED]")

	// Truncate if too long
	if len(str) > maxLen {
		str = str[:maxLen] + "..."
	}

	return []byte(str)
}

func sanitizeDiagnosticText(text string, maxLen int) string {
	text = imageDataURLRegex.ReplaceAllString(text, "[IMAGE DATA REDACTED]")
	text = longBase64Regex.ReplaceAllString(text, "[ENCODED DATA REDACTED]")
	text = string(sanitizeResponseBody([]byte(text), max(len(text)*2, maxLen)))
	text = strings.TrimSpace(strings.ToValidUTF8(text, ""))

	runes := []rune(text)
	if len(runes) > maxLen {
		text = string(runes[:maxLen]) + "..."
	}

	return text
}

func responsesDiagnostic(response *httpclient.Response) (*responsesExecutionDiagnostic, string, bool) {
	if response == nil || len(response.Body) == 0 {
		return nil, "", false
	}
	if response.Request != nil && response.Request.APIFormat != "" && response.Request.APIFormat != llm.APIFormatOpenAIResponse.String() {
		return nil, "", false
	}

	var wire struct {
		Object            string  `json:"object"`
		ID                string  `json:"id"`
		Status            *string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string  `json:"type"`
			Text    *string `json:"text"`
			Refusal *string `json:"refusal"`
			Content []struct {
				Type    string  `json:"type"`
				Text    *string `json:"text"`
				Refusal *string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
		Error *responsesDiagnosticError `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &wire); err != nil {
		return nil, "", false
	}
	if (response.Request == nil || response.Request.APIFormat == "") && wire.Object != "response" {
		return nil, "", false
	}
	if wire.Object != "response" && wire.ID == "" && wire.Status == nil && len(wire.Output) == 0 && wire.Error == nil {
		return nil, "", false
	}

	diagnostic := &responsesExecutionDiagnostic{Status: "unknown"}
	if wire.Status != nil && strings.TrimSpace(*wire.Status) != "" {
		diagnostic.Status = sanitizeDiagnosticText(*wire.Status, 64)
	}
	if wire.IncompleteDetails != nil {
		diagnostic.IncompleteReason = sanitizeDiagnosticText(wire.IncompleteDetails.Reason, 128)
	}
	if wire.Error != nil {
		diagnostic.Error = &responsesDiagnosticError{
			Type:    sanitizeDiagnosticText(wire.Error.Type, 128),
			Code:    sanitizeDiagnosticText(wire.Error.Code, 128),
			Message: sanitizeDiagnosticText(wire.Error.Message, 512),
		}
	}

	for _, item := range wire.Output {
		diagnostic.OutputTypes = append(diagnostic.OutputTypes, sanitizeDiagnosticText(item.Type, 64))
		if diagnostic.RefusalSummary == "" && item.Refusal != nil {
			diagnostic.RefusalSummary = sanitizeDiagnosticText(*item.Refusal, 512)
		}
		if diagnostic.MessageSummary == "" && item.Text != nil {
			diagnostic.MessageSummary = sanitizeDiagnosticText(*item.Text, 512)
		}
		for _, content := range item.Content {
			if diagnostic.RefusalSummary == "" && content.Type == "refusal" && content.Refusal != nil {
				diagnostic.RefusalSummary = sanitizeDiagnosticText(*content.Refusal, 512)
			}
			if diagnostic.MessageSummary == "" && content.Type == "output_text" && content.Text != nil {
				diagnostic.MessageSummary = sanitizeDiagnosticText(*content.Text, 512)
			}
		}
	}

	return diagnostic, wire.ID, true
}

func sanitizedResponsesDiagnosticError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}

	message := sanitizeDiagnosticText(ExtractErrorMessage(err), 512)
	if responseErr, ok := xerrors.As[*llm.ResponseError](err); ok {
		sanitized := *responseErr
		sanitized.Detail = responseErr.Detail
		sanitized.Detail.Message = message

		return &sanitized
	}

	return errors.New(message)
}

// persistRequestExecutionMiddleware ensures a request execution exists and handles error updates.
type persistRequestExecutionMiddleware struct {
	pipeline.DummyMiddleware

	outbound *PersistentOutboundTransformer

	rawResponse *httpclient.Response
}

func persistRequestExecution(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return &persistRequestExecutionMiddleware{
		outbound: outbound,
	}
}

func (m *persistRequestExecutionMiddleware) Name() string {
	return "persist-request-execution"
}

func (m *persistRequestExecutionMiddleware) OnOutboundRawRequest(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	m.rawResponse = nil

	state := m.outbound.state
	if state == nil || state.RequestExec != nil {
		return request, nil
	}

	channel := m.outbound.GetCurrentChannel()
	if channel == nil {
		return request, nil
	}

	candidate := state.ChannelModelsCandidates[state.CurrentCandidateIndex]
	entry := candidate.Models[state.CurrentModelIndex]

	// Prefer the API format of the actual outbound request: transformers may emit
	// multiple formats (e.g. OpenAI outbound also builds audio speech/transcription
	// requests) while APIFormat() only reports the primary one.
	format := m.outbound.APIFormat()
	if request.APIFormat != "" {
		format = llm.APIFormat(request.APIFormat)
	}

	requestExec, err := state.RequestService.CreateRequestExecution(
		ctx,
		channel,
		entry.ActualModel,
		state.Request,
		*request,
		format,
		state.RequestedServiceTier,
		state.SpeedMode,
		state.PassThroughApplied,
	)
	if err != nil {
		return nil, err
	}

	// Update request with channel ID after channel selection
	if state.Request != nil && state.Request.ChannelID != channel.ID {
		err := state.RequestService.UpdateRequestChannelID(ctx, state.Request.ID, channel.ID)
		if err != nil {
			return nil, err
		}
		// Update the in-memory state to prevent duplicate updates and ensure consistency
		state.Request.ChannelID = channel.ID
	}

	state.RequestExec = requestExec

	return request, nil
}

func (m *persistRequestExecutionMiddleware) OnOutboundRawResponse(ctx context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	m.rawResponse = response
	return response, nil
}

func (m *persistRequestExecutionMiddleware) OnOutboundLlmResponse(ctx context.Context, llmResp *llm.Response) (*llm.Response, error) {
	state := m.outbound.state
	if state == nil || state.RequestExec == nil {
		return llmResp, nil
	}

	// Use context without cancellation to ensure persistence even if client canceled
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Build latency metrics from performance record
	var metrics *biz.LatencyMetrics

	if state.Perf != nil && !state.Perf.StartTime.IsZero() {
		var (
			firstTokenLatencyMs int64
			requestLatencyMs    int64
		)

		if state.Perf.RequestCompleted && !state.Perf.EndTime.IsZero() {
			firstTokenLatencyMs, requestLatencyMs, _ = state.Perf.Calculate()
		} else {
			requestLatencyMs = time.Since(state.Perf.StartTime).Milliseconds()
			if state.Perf.Stream && state.Perf.FirstTokenTime != nil {
				firstTokenLatencyMs = state.Perf.FirstTokenTime.Sub(state.Perf.StartTime).Milliseconds()
			}

			requestLatencyMs = biz.ClampLatency(requestLatencyMs)
			firstTokenLatencyMs = biz.ClampLatency(firstTokenLatencyMs)
		}

		metrics = &biz.LatencyMetrics{
			LatencyMs: &requestLatencyMs,
		}
		if state.Perf.Stream && state.Perf.FirstTokenTime != nil {
			metrics.FirstTokenLatencyMs = &firstTokenLatencyMs
		}

		if state.Perf.Stream {
			reasoningDurationMs := state.Perf.CalculateReasoningDurationMs()
			if reasoningDurationMs > 0 {
				metrics.ReasoningDurationMs = &reasoningDurationMs
			}
		}
	}

	if terminalErr := responsePersistenceTerminalError(llmResp); terminalErr != nil {
		var responseBody []byte
		if m.rawResponse != nil {
			responseBody = m.rawResponse.Body
		}

		err := state.RequestService.UpdateRequestExecutionTerminated(
			persistCtx,
			state.RequestExec.ID,
			terminalErr,
			llmResp.ID,
			responseBody,
			metrics,
		)
		if err != nil {
			log.Warn(persistCtx, "Failed to persist terminal request execution response", log.Cause(err))
		}

		return llmResp, nil
	}

	// Audio responses (binary TTS / non-JSON STT) must be converted to JSON-safe payloads
	// before persisting into the JSON response_body column.
	respBody := audioSafeResponseBody(llmResp.RequestType, m.rawResponse.Headers.Get("Content-Type"), m.rawResponse.Body)

	err := state.RequestService.UpdateRequestExecutionCompleted(
		persistCtx,
		state.RequestExec.ID,
		llmResp.ID,
		respBody,
		metrics,
	)
	if err != nil {
		log.Warn(persistCtx, "Failed to update request execution status to completed", log.Cause(err))
	}

	return llmResp, nil
}

func (m *persistRequestExecutionMiddleware) OnOutboundRawError(ctx context.Context, err error) {
	// Update request execution with the real error message when request fails
	state := m.outbound.state
	if state == nil || state.RequestExec == nil {
		return
	}

	// Log error with channel information for better debugging
	channel := m.outbound.GetCurrentChannel()
	if channel != nil {
		logFields := []log.Field{
			log.Cause(err),
			log.Int("channel_id", channel.ID),
			log.String("channel_name", channel.Name),
		}
		if modelID := m.outbound.GetCurrentModelID(); modelID != "" {
			logFields = append(logFields, log.String("model_id", modelID))
		}
		// Add response body for HTTP errors to help debug 400 errors (sanitized for PII)
		if httpErr, ok := xerrors.As[*httpclient.Error](err); ok && len(httpErr.Body) > 0 {
			sanitizedBody := sanitizeResponseBody(httpErr.Body, 1024)
			logFields = append(logFields, log.ByteString("response_body", sanitizedBody))
		}

		log.Warn(ctx, "request process failed", logFields...)
	}

	// Use context without cancellation to ensure persistence even if client canceled
	persistCtx, cancel := xcontext.DetachWithTimeout(ctx, 10*time.Second)
	defer cancel()

	var updateErr error
	if diagnostic, externalID, ok := responsesDiagnostic(m.rawResponse); ok {
		updateErr = state.RequestService.UpdateRequestExecutionTerminated(
			persistCtx,
			state.RequestExec.ID,
			sanitizedResponsesDiagnosticError(err),
			externalID,
			diagnostic,
			latencyMetrics(state.Perf),
		)
	} else {
		updateErr = state.RequestService.UpdateRequestExecutionFailed(
			persistCtx,
			state.RequestExec.ID,
			ExtractErrorMessage(err),
			ExtractErrorInfo(err),
		)
	}
	if updateErr != nil {
		log.Warn(persistCtx, "Failed to update request execution status to failed", log.Cause(updateErr))
	}
}

// ExtractErrorInfo extracts HTTP status code and sanitized response body from error.
func ExtractErrorInfo(err error) *biz.ExecutionErrorInfo {
	httpErr, ok := xerrors.As[*httpclient.Error](err)
	if !ok {
		return nil
	}

	return &biz.ExecutionErrorInfo{
		StatusCode: &httpErr.StatusCode,
	}
}

// ExtractErrorMessage extracts HTTP error message from error.
func ExtractErrorMessage(err error) string {
	if responseErr, ok := xerrors.As[*llm.ResponseError](err); ok && responseErr.Detail.Message != "" {
		return responseErr.Detail.Message
	}

	httpErr, ok := xerrors.As[*httpclient.Error](err)
	if !ok {
		return err.Error()
	}

	// Anthropic && OpenAI error format.
	message := gjson.GetBytes(httpErr.Body, "error.message")
	if message.Exists() && message.Type == gjson.String {
		return message.String()
	}

	// Other compatible error format.
	// Try errors.0.message first, then fall back to errors.message
	message1 := gjson.GetBytes(httpErr.Body, "errors.0.message")
	message2 := gjson.GetBytes(httpErr.Body, "errors.message")

	if message1.Exists() && message1.Type == gjson.String && message1.String() != "" {
		return message1.String()
	}

	if message2.Exists() && message2.Type == gjson.String {
		return message2.String()
	}

	return httpErr.Error()
}

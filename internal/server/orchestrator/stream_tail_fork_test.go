package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestStreamTailForkPreservesTerminalEvidenceAndTier(t *testing.T) {
	for _, test := range []struct {
		name       string
		typeName   string
		status     string
		detail     string
		wantStatus requestexecution.Status
		wantError  string
	}{
		{"failed", "response.failed", "failed", `,"error":{"code":"quota_exceeded","message":"quota exhausted","type":"rate_limit_error"}`, requestexecution.StatusFailed, "quota exhausted"},
		{"incomplete", "response.incomplete", "incomplete", `,"incomplete_details":{"reason":"max_output_tokens"}`, requestexecution.StatusFailed, "max_output_tokens"},
		{"canceled", "response.canceled", "canceled", "", requestexecution.StatusCanceled, "canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
			_, requestService, systemService, usageService := setupTestServices(t, client)
			require.NoError(t, systemService.SetStoragePolicy(ctx, &biz.StoragePolicy{StoreChunks: true, StoreResponseBody: true}))
			req, execution, state := newTask5Execution(t, ctx, client, llm.APIFormatOpenAIResponse.String())
			state.RequestedServiceTier = "priority"
			outbound, err := responses.NewOutboundTransformer("https://example.test", "synthetic-key")
			require.NoError(t, err)
			stream := NewOutboundPersistentStream(ctx, streams.SliceStream([]*httpclient.StreamEvent{}), req, execution, requestService, usageService, outbound, nil, state)
			body := []byte(`{"type":"` + test.typeName + `","response":{"id":"resp_tail","model":"gpt-5","status":"` + test.status + `","service_tier":"ultrafast","output":[],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}` + test.detail + `}}`)
			require.NoError(t, stream.consumeTailEvent(&httpclient.StreamEvent{Type: test.typeName, Data: body}))
			require.NoError(t, stream.FinalizeAccounting(llm.SourceEndCanceled))

			saved, err := client.RequestExecution.Get(context.WithoutCancel(ctx), execution.ID)
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, saved.Status)
			require.Contains(t, saved.ErrorMessage, test.wantError)
			require.Contains(t, string(saved.ResponseBody), "resp_tail")
			require.NotEmpty(t, saved.ResponseChunks)
			require.Error(t, state.StreamTerminalError)
			require.JSONEq(t, string(body), string(state.StreamTerminalBody))
			require.True(t, state.AccountingFinal)
			usage, err := client.UsageLog.Query().Only(ctx)
			require.NoError(t, err)
			require.Equal(t, "priority", usage.RequestedServiceTier)
			require.Equal(t, "ultrafast", usage.AppliedServiceTier)
			require.EqualValues(t, 15, usage.TotalTokens)
		})
	}
}

package responses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/internal/pkg/xtest"
)

func TestTransformRequest_Integration(t *testing.T) {
	inboundTransformer := NewInboundTransformer()
	outboundTransformer, _ := NewOutboundTransformer("https://api.openai.com", "test-api-key")

	tests := []struct {
		name         string
		requestFile  string
		expectedFile string
	}{
		{
			name:         "simple request array",
			requestFile:  `simple.request.json`,
			expectedFile: `simple.request.json`,
		},
		{
			name:         "single array",
			requestFile:  `single_array.request.json`,
			expectedFile: `single_array.request.json`,
		},
		{
			name:         "reasoning request",
			requestFile:  `reasoning.request.json`,
			expectedFile: `reasoning.request.json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inputReq Request

			err := xtest.LoadTestData(t, tt.requestFile, &inputReq)
			require.NoError(t, err)

			var expectedReq Request

			err = xtest.LoadTestData(t, tt.expectedFile, &expectedReq)
			require.NoError(t, err)

			var buf bytes.Buffer

			decoder := json.NewEncoder(&buf)
			decoder.SetEscapeHTML(false)

			if err := decoder.Encode(inputReq); err != nil {
				t.Fatalf("failed to marshal tool result: %v", err)
			}

			chatReq, err := inboundTransformer.TransformRequest(t.Context(), &httpclient.Request{
				Headers: http.Header{
					"Content-Type": []string{"application/json"},
				},
				Body: buf.Bytes(),
			})
			require.NoError(t, err)
			require.NotNil(t, chatReq)

			outboundReq, err := outboundTransformer.TransformRequest(t.Context(), chatReq)
			require.NoError(t, err)

			var gotReq Request

			err = json.Unmarshal(outboundReq.Body, &gotReq)
			require.NoError(t, err)

			if !xtest.Equal(expectedReq, gotReq, cmpopts.IgnoreFields(Item{}, "EncryptedContent")) {
				t.Errorf("wantReq != gotReq\n%s", cmp.Diff(expectedReq, gotReq))
			}
		})
	}
}

func TestTransformRequest_NormalizesCodexAutomationBootstrap(t *testing.T) {
	inbound := NewInboundTransformer()
	outbound, err := NewOutboundTransformer("https://api.openai.com", "test-api-key")
	require.NoError(t, err)

	llmReq, err := inbound.TransformRequest(t.Context(), &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body: []byte(`{
			"model": "gpt-5.6-sol",
			"store": false,
			"input": [{
				"type": "function_call_output",
				"id": "fco_automation_bootstrap",
				"name": "automation_update",
				"output": "Automation: Daily Skills maintenance"
			}]
		}`),
	})
	require.NoError(t, err)
	require.Len(t, llmReq.Messages, 1)
	require.Equal(t, "user", llmReq.Messages[0].Role)
	require.Equal(t, "Automation: Daily Skills maintenance", *llmReq.Messages[0].Content.Content)

	httpReq, err := outbound.TransformRequest(t.Context(), llmReq)
	require.NoError(t, err)

	var got Request
	require.NoError(t, json.Unmarshal(httpReq.Body, &got))
	require.Len(t, got.Input.Items, 1)
	require.Equal(t, "message", got.Input.Items[0].Type)
	require.Equal(t, "user", got.Input.Items[0].Role)
	require.Len(t, got.Input.Items[0].Content.Items, 1)
	require.Equal(t, "Automation: Daily Skills maintenance", *got.Input.Items[0].Content.Items[0].Text)
}

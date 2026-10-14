package pipeline

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
)

func TestInlineToolResultCommitsResponseForkCompatibility(t *testing.T) {
	require.True(t, hasResponseContent(&llm.Response{Choices: []llm.Choice{{
		Delta: &llm.Message{InlineToolResults: []llm.InlineToolResult{{ToolCallID: "call_1"}}},
	}}}))
}

package llm

const (
	// TransformerMetadataKeyAnthropicType stores the original Anthropic content
	// block type when a provider bridge needs to re-emit it.
	TransformerMetadataKeyAnthropicType = "anthropic_type"

	// TransformerMetadataKeyAnthropicCaller stores the optional Anthropic caller
	// object for server-side tool blocks.
	TransformerMetadataKeyAnthropicCaller = "anthropic_caller"

	// TransformerMetadataKeyAnthropicToolResultContent stores raw Anthropic
	// tool_result content bytes for inline result round-tripping.
	TransformerMetadataKeyAnthropicToolResultContent = "anthropic_tool_result_content"

	// TransformerMetadataKeyAnthropicBlockIndex stores the original Anthropic
	// content-block ordinal position.
	TransformerMetadataKeyAnthropicBlockIndex = "anthropic_block_index"

	// TransformerMetadataKeyAnthropicFunctionToolSearchName stores the original
	// Anthropic function tool name bridged to OpenAI Responses tool_search.
	TransformerMetadataKeyAnthropicFunctionToolSearchName = "anthropic_function_tool_search_name"

	// TransformerMetadataKeyOpenAIResponsesToolResultItemType overrides the
	// Responses input item type used when rendering a tool-role message.
	TransformerMetadataKeyOpenAIResponsesToolResultItemType = "openai_responses_tool_result_item_type"

	// TransformerMetadataKeyOpenAIResponsesToolSearchExecution carries the
	// Responses tool_search execution mode for bridged calls/results.
	TransformerMetadataKeyOpenAIResponsesToolSearchExecution = "openai_responses_tool_search_execution"
)

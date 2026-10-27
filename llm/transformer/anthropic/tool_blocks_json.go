package anthropic

import "encoding/json"

// UnmarshalJSON preserves server-tool result objects (including search errors).
// Ordinary message and client-tool content still uses MessageContent validation.
func (b *MessageContentBlock) UnmarshalJSON(data []byte) error {
	type blockAlias MessageContentBlock
	var decoded blockAlias
	var wire struct {
		*blockAlias

		Content json.RawMessage `json:"content"`
	}
	wire.blockAlias = &decoded
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*b = MessageContentBlock(decoded)
	if b.Type == "tool_search_tool_result" {
		// 保留完整搜索结果块，供流式聚合及跨协议工具搜索回传使用。
		b.ServerContent = append(json.RawMessage(nil), data...)
	}
	b.Content = nil
	if len(wire.Content) == 0 || string(wire.Content) == "null" {
		return nil
	}
	b.Content = new(MessageContent)
	if isAnthropicSpecialToolResultBlock(b.Type) {
		b.Content.SetRaw(wire.Content)
		return nil
	}
	return json.Unmarshal(wire.Content, b.Content)
}

package llm

type TransformOptions struct {
	// ArrayInstructions specifies whether the system instructions is an array.
	ArrayInstructions *bool `json:"array_instructions,omitempty"`

	// ArrayInputs specifies whether the inputs is an array.
	ArrayInputs *bool `json:"array_inputs,omitempty"`

	// CodexStyleResponses makes the Codex transformer emit a fuller native
	// Codex Responses request shape for bridged requests.
	CodexStyleResponses *bool `json:"codex_style_responses,omitempty"`
}

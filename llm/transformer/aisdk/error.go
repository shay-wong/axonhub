package aisdk

import "encoding/json"

type errorResponse struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Truncated bool   `json:"truncated,omitempty"`
}

func marshalErrorResponse(message string, errorType string, truncated bool) []byte {
	body, _ := json.Marshal(errorResponse{
		Message:   message,
		Type:      errorType,
		Truncated: truncated,
	})

	return body
}

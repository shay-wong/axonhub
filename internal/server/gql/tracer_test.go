package gql

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactGraphQLVariablesForLog(t *testing.T) {
	variables := map[string]any{
		"input": map[string]any{
			"name": "OpenCode Go",
			"settings": map[string]any{
				"providerQuota": map[string]any{
					"opencodeGo": map[string]any{
						"workspaceId":  "wk_123",
						"authCookie":   "secret-cookie",
						"Auth_Cookie":  "case-secret-cookie",
						"refreshToken": "refresh-secret",
					},
				},
				"headerOverrideOperations": []any{
					map[string]any{
						"op":    "set",
						"path":  "/headers/authorization",
						"value": "Bearer header-token",
					},
				},
				"bodyOverrideOperations": []any{
					map[string]any{
						"op":   "replace",
						"path": "/credential/api-key",
						"match": map[string]any{
							"path": "/credential/api-key",
							"eq":   "body-secret",
						},
						"value": "body-replacement-secret",
					},
				},
			},
			"credentials": map[string]any{
				"apiKeys": []any{"key-1", "key-2"},
			},
			"overrideHeaders": []any{
				map[string]any{
					"key":   "Cookie",
					"value": "auth=header-cookie",
				},
			},
		},
		"filters": []any{
			map[string]any{"authorization": "Bearer token"},
			map[string]any{"name": "safe"},
		},
	}

	redacted := redactGraphQLVariablesForLog(variables)
	encoded, err := json.Marshal(redacted)
	require.NoError(t, err)
	encodedText := string(encoded)

	require.NotContains(t, encodedText, "secret-cookie")
	require.NotContains(t, encodedText, "case-secret-cookie")
	require.NotContains(t, encodedText, "refresh-secret")
	require.NotContains(t, encodedText, "authCookie")
	require.NotContains(t, encodedText, "Auth_Cookie")
	require.NotContains(t, encodedText, "refreshToken")
	require.NotContains(t, encodedText, "Bearer token")
	require.NotContains(t, encodedText, "key-1")
	require.NotContains(t, encodedText, "apiKeys")
	require.NotContains(t, encodedText, "auth=header-cookie")
	require.NotContains(t, encodedText, "Cookie")
	require.NotContains(t, encodedText, "authorization")
	require.NotContains(t, encodedText, "Bearer header-token")
	require.NotContains(t, encodedText, "api-key")
	require.NotContains(t, encodedText, "body-secret")
	require.NotContains(t, encodedText, "body-replacement-secret")
	require.Contains(t, encodedText, redactedGraphQLVariableValue)
	require.Contains(t, encodedText, "wk_123")
	require.Contains(t, encodedText, "safe")

	require.Equal(t, "secret-cookie", variables["input"].(map[string]any)["settings"].(map[string]any)["providerQuota"].(map[string]any)["opencodeGo"].(map[string]any)["authCookie"])
}

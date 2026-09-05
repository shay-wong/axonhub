package gql

import (
	"context"
	"fmt"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"go.uber.org/zap"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/tracing"
)

type loggingTracer struct{}

const redactedGraphQLVariableValue = "[REDACTED]"
const redactedGraphQLVariableKey = "[REDACTED_KEY]"

var sensitiveGraphQLVariableKeys = map[string]struct{}{
	"accesstoken":   {},
	"apikey":        {},
	"apikeys":       {},
	"authcookie":    {},
	"authorization": {},
	"clientsecret":  {},
	"cookie":        {},
	"jsondata":      {},
	"key":           {},
	"password":      {},
	"privatekey":    {},
	"refreshtoken":  {},
	"secret":        {},
	"setcookie":     {},
	"token":         {},
}

var graphQLVariableSecretDescriptorKeys = map[string]struct{}{
	"from": {},
	"key":  {},
	"name": {},
	"path": {},
	"to":   {},
}

var graphQLVariableSecretPayloadKeys = map[string]struct{}{
	"eq":          {},
	"replacement": {},
	"value":       {},
	"values":      {},
}

var _ interface {
	graphql.HandlerExtension
	graphql.ResponseInterceptor
} = &loggingTracer{}

func (t *loggingTracer) ExtensionName() string {
	return "logging_tracer"
}

func (t *loggingTracer) Validate(schema graphql.ExecutableSchema) error {
	return nil
}

func (t *loggingTracer) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	if graphql.HasOperationContext(ctx) {
		opCtx := graphql.GetOperationContext(ctx)
		ctx = tracing.WithOperationName(ctx, opCtx.OperationName)

		if log.DebugEnabled(ctx) {
			// The raw query text is intentionally not logged: GraphQL clients
			// may inline sensitive literals (e.g. a channel quota cookie)
			// instead of passing them as variables, so only the operation name
			// and redacted variables are recorded.
			log.Debug(ctx, "received graphql request",
				zap.String("operation_name", opCtx.OperationName),
				zap.Any("variables", redactGraphQLVariablesForLog(opCtx.Variables)),
			)
		}
	}

	resp := next(ctx)

	// Capture GraphQL errors to context for access logging
	if resp != nil && len(resp.Errors) > 0 {
		for _, gqlErr := range resp.Errors {
			contexts.AddError(ctx, gqlErr)
		}
	}

	return resp
}

func redactGraphQLVariablesForLog(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redactPayload := hasSensitiveGraphQLVariableDescriptor(typed)
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveGraphQLVariableKey(key) {
				redacted[nextRedactedGraphQLVariableKey(redacted)] = redactedGraphQLVariableValue
				continue
			}

			if (redactPayload && isGraphQLVariableSecretPayloadKey(key)) || graphQLVariableDescriptorReferencesSecret(key, item) {
				redacted[key] = redactedGraphQLVariableValue
				continue
			}

			redacted[key] = redactGraphQLVariablesForLog(item)
		}

		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactGraphQLVariablesForLog(item)
		}

		return redacted
	default:
		return value
	}
}

func nextRedactedGraphQLVariableKey(values map[string]any) string {
	if _, exists := values[redactedGraphQLVariableKey]; !exists {
		return redactedGraphQLVariableKey
	}

	for i := 2; ; i++ {
		key := fmt.Sprintf("%s_%d", redactedGraphQLVariableKey, i)
		if _, exists := values[key]; !exists {
			return key
		}
	}
}

func isSensitiveGraphQLVariableKey(key string) bool {
	normalized := normalizeGraphQLVariableSensitiveKey(key)
	_, ok := sensitiveGraphQLVariableKeys[normalized]
	return ok
}

func hasSensitiveGraphQLVariableDescriptor(values map[string]any) bool {
	for key, value := range values {
		if graphQLVariableDescriptorReferencesSecret(key, value) {
			return true
		}
	}

	return false
}

func graphQLVariableDescriptorReferencesSecret(key string, value any) bool {
	if !isGraphQLVariableSecretDescriptorKey(key) {
		return false
	}

	text, ok := value.(string)
	return ok && stringReferencesSensitiveGraphQLVariable(text)
}

func isGraphQLVariableSecretDescriptorKey(key string) bool {
	normalized := normalizeGraphQLVariableSensitiveKey(key)
	_, ok := graphQLVariableSecretDescriptorKeys[normalized]
	return ok
}

func isGraphQLVariableSecretPayloadKey(key string) bool {
	normalized := normalizeGraphQLVariableSensitiveKey(key)
	_, ok := graphQLVariableSecretPayloadKeys[normalized]
	return ok
}

func stringReferencesSensitiveGraphQLVariable(value string) bool {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '/', '.', '-', '_', ' ', '[', ']', ':':
			return true
		default:
			return false
		}
	})

	for _, part := range parts {
		if part == "" {
			continue
		}

		if isSensitiveGraphQLVariableKey(part) {
			return true
		}
	}

	return false
}

func normalizeGraphQLVariableSensitiveKey(key string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
}

package gql

import (
	"context"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/scopes"
)

func requestExecutionChannelAPIKeyName(ctx context.Context, obj *ent.RequestExecution) *string {
	if !scopes.UserHasScope(ctx, scopes.ScopeWriteChannels) || obj == nil || obj.ChannelAPIKeyName == "" {
		return nil
	}

	return &obj.ChannelAPIKeyName
}

func requestExecutionChannelAPIKeySuffix(ctx context.Context, obj *ent.RequestExecution) *string {
	if !scopes.UserHasScope(ctx, scopes.ScopeWriteChannels) || obj == nil || obj.ChannelAPIKeySuffix == "" {
		return nil
	}

	return &obj.ChannelAPIKeySuffix
}

func requestExecutionChannelAPIKeyHeaders(ctx context.Context, obj *ent.RequestExecution) []string {
	if !scopes.UserHasScope(ctx, scopes.ScopeWriteChannels) || obj == nil || len(obj.ChannelAPIKeyHeaders) == 0 {
		return nil
	}

	return obj.ChannelAPIKeyHeaders
}

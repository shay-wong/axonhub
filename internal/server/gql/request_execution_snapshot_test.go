package gql

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/scopes"
)

func TestRequestExecutionAPIKeySnapshotRequiresWriteChannels(t *testing.T) {
	execution := &ent.RequestExecution{
		ChannelAPIKeyName:    "Primary Account",
		ChannelAPIKeySuffix:  "1234",
		ChannelAPIKeyHeaders: []string{"Authorization"},
	}
	resolver := &requestExecutionResolver{}

	readRequestsCtx := contexts.WithUser(context.Background(), &ent.User{Scopes: []string{string(scopes.ScopeReadRequests)}})
	name, err := resolver.ChannelAPIKeyName(readRequestsCtx, execution)
	require.NoError(t, err)
	require.Nil(t, name)
	suffix, err := resolver.ChannelAPIKeySuffix(readRequestsCtx, execution)
	require.NoError(t, err)
	require.Nil(t, suffix)
	headers, err := resolver.ChannelAPIKeyHeaders(readRequestsCtx, execution)
	require.NoError(t, err)
	require.Nil(t, headers)

	writeChannelsCtx := contexts.WithUser(context.Background(), &ent.User{Scopes: []string{string(scopes.ScopeWriteChannels)}})
	name, err = resolver.ChannelAPIKeyName(writeChannelsCtx, execution)
	require.NoError(t, err)
	require.Equal(t, "Primary Account", *name)
	suffix, err = resolver.ChannelAPIKeySuffix(writeChannelsCtx, execution)
	require.NoError(t, err)
	require.Equal(t, "1234", *suffix)
	headers, err = resolver.ChannelAPIKeyHeaders(writeChannelsCtx, execution)
	require.NoError(t, err)
	require.Equal(t, []string{"Authorization"}, headers)
}

func TestRequestExecutionAPIKeySnapshotIsNotFilterable(t *testing.T) {
	schema, err := os.ReadFile("ent.graphql")
	require.NoError(t, err)
	schemaText := string(schema)
	start := strings.Index(schemaText, "input RequestExecutionWhereInput {")
	require.NotEqual(t, -1, start)
	end := strings.Index(schemaText[start:], "\n}")
	require.NotEqual(t, -1, end)
	whereInput := schemaText[start : start+end]
	require.NotContains(t, whereInput, "channelAPIKeyName")
	require.NotContains(t, whereInput, "channelAPIKeySuffix")
	require.NotContains(t, whereInput, "channelAPIKeyHeaders")
}

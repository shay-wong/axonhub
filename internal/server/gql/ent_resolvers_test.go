package gql

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
)

func TestUsageLogResolverRequestExecutionID(t *testing.T) {
	resolver := &usageLogResolver{}

	guid, err := resolver.RequestExecutionID(t.Context(), &ent.UsageLog{})
	require.NoError(t, err)
	require.Nil(t, guid)

	guid, err = resolver.RequestExecutionID(t.Context(), &ent.UsageLog{RequestExecutionID: 42})
	require.NoError(t, err)
	require.Equal(t, ent.TypeRequestExecution, guid.Type)
	require.Equal(t, 42, guid.ID)
}

func TestPromptResolverProjectID(t *testing.T) {
	projectID, err := (&promptResolver{}).ProjectID(t.Context(), &ent.Prompt{ProjectID: 42})

	require.NoError(t, err)
	require.Equal(t, ent.TypeProject, projectID.Type)
	require.Equal(t, 42, projectID.ID)
}

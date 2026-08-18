package datamigrate

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/stretchr/testify/require"
)

func TestV1_0_0_Beta9_SkipsMonotonicCleanupForPostgres(t *testing.T) {
	// A nil client makes the test fail if this path ever attempts SQL execution.
	require.NoError(t, NewV1_0_0_Beta9().stripMonotonicUpdatedAt(
		context.Background(), nil, dialect.Postgres,
	))
}

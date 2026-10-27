package qb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDistinctOnPreservesProductionExecutionModel(t *testing.T) {
	queries := map[string]string{
		"ranking":           BuildThroughputQuery(true, ThroughputQueryByModel, 10, ThroughputModeDistinctOn),
		"daily ranking":     BuildDailyModelThroughputQuery("postgres", "UTC", 0, 10, ThroughputModeDistinctOn),
		"daily performance": BuildDailyPerformanceStatsQuery("postgres", "UTC", 0, DailyThroughputByModel, "$1", ThroughputModeDistinctOn),
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, query, "DISTINCT ON")
			require.Contains(t, query, "se.model_id")
			require.Contains(t, query, "se.source <> 'test'")
			require.Contains(t, query, "ul.source <> 'test'")
			require.NotContains(t, query, "JOIN requests")
		})
	}
}

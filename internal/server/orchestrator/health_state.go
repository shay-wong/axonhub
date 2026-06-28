package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/contexts"
	entrequest "github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
)

func shouldSkipHealthStateTracking(ctx context.Context) bool {
	source, ok := contexts.GetSource(ctx)
	return ok && source == entrequest.SourceTest
}

func shouldSkipHealthStateTrackingForState(ctx context.Context, state *PersistenceState) bool {
	if state != nil {
		if state.Request != nil && state.Request.Source == entrequest.SourceTest {
			return true
		}
		if state.RequestExec != nil && state.RequestExec.Source == requestexecution.SourceTest {
			return true
		}
	}

	return shouldSkipHealthStateTracking(ctx)
}

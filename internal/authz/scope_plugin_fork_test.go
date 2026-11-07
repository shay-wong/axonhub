package authz

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/privacy"
	"github.com/looplj/axonhub/internal/scopes"
)

func TestHasSystemScope_PluginUsesSystemGrantIntersection(t *testing.T) {
	for _, tt := range []struct {
		name   string
		user   *ent.User
		grants []string
		allow  bool
	}{
		{"system scope and grant", &ent.User{ID: 1, Scopes: []string{"read_dashboard"}}, []string{"read_dashboard"}, true},
		{"system scope without grant", &ent.User{ID: 1, Scopes: []string{"read_dashboard"}}, nil, false},
		{"grant without user scope", &ent.User{ID: 1}, []string{"read_dashboard"}, false},
		{"owner with grant", &ent.User{ID: 1, IsOwner: true}, []string{"read_dashboard"}, true},
		{"owner without grant", &ent.User{ID: 1, IsOwner: true}, nil, false},
		{"project membership scope", &ent.User{ID: 1, Edges: ent.UserEdges{
			ProjectUsers: []*ent.UserProject{{ProjectID: 100, Scopes: []string{"read_dashboard"}}},
		}}, []string{"read_dashboard"}, false},
		{"project role scope", &ent.User{ID: 1, Edges: ent.UserEdges{
			ProjectUsers: []*ent.UserProject{{ProjectID: 100}},
			Roles:        []*ent.Role{{ProjectID: lo.ToPtr(100), Scopes: []string{"read_dashboard"}}},
		}}, []string{"read_dashboard"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := contexts.WithUser(NewUserContext(context.Background(), tt.user.ID), tt.user)
			parent = contexts.WithProjectID(parent, 100)
			ctx, err := DelegatePlugin(parent, 10, tt.grants)
			require.NoError(t, err)
			require.Equal(t, tt.allow, HasSystemScope(ctx, scopes.ScopeReadDashboard))
			require.Equal(t, tt.allow, RequireSystemScope(ctx, scopes.ScopeReadDashboard) == nil)
			decision, exists := privacy.DecisionFromContext(WithSystemScopeDecision(ctx, scopes.ScopeReadDashboard))
			require.True(t, exists)
			require.Equal(t, tt.allow, decision == nil)
			principal, _ := GetPrincipal(ctx)
			require.Equal(t, PrincipalTypePlugin, principal.Type)
			require.False(t, IsBypassActive(ctx))
		})
	}
}

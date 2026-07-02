package scopes_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/privacy"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/scopes"
)

func TestUserPersonalAPIKeyReadRule_SystemScopeWithoutProjectID(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:personal_apikey_system_scope?mode=memory&_fk=0")
	defer client.Close()

	setupCtx := ent.NewContext(t.Context(), client)
	setupCtx = privacy.DecisionContext(setupCtx, privacy.Allow)

	projectRow, err := client.Project.Create().
		SetName("APIKey Policy Project").
		SetDescription("api key policy test project").
		SetStatus(project.StatusActive).
		Save(setupCtx)
	require.NoError(t, err)

	adminUser, err := client.User.Create().
		SetEmail("admin@example.com").
		SetPassword("password").
		SetStatus(user.StatusActivated).
		SetScopes([]string{string(scopes.ScopeReadAPIKeys)}).
		Save(setupCtx)
	require.NoError(t, err)

	otherUser, err := client.User.Create().
		SetEmail("other-admin@example.com").
		SetPassword("password").
		SetStatus(user.StatusActivated).
		Save(setupCtx)
	require.NoError(t, err)

	serviceKey, err := client.APIKey.Create().
		SetProjectID(projectRow.ID).
		SetUserID(otherUser.ID).
		SetKey("visible-service-key").
		SetName("visible-service-key").
		SetType(apikey.TypeServiceAccount).
		SetStatus(apikey.StatusEnabled).
		Save(setupCtx)
	require.NoError(t, err)

	adminPersonalKey, err := client.APIKey.Create().
		SetProjectID(projectRow.ID).
		SetUserID(adminUser.ID).
		SetKey("own-personal-key").
		SetName("own-personal-key").
		SetType(apikey.TypePersonal).
		SetStatus(apikey.StatusEnabled).
		Save(setupCtx)
	require.NoError(t, err)

	_, err = client.APIKey.Create().
		SetProjectID(projectRow.ID).
		SetUserID(otherUser.ID).
		SetKey("other-personal-key").
		SetName("other-personal-key").
		SetType(apikey.TypePersonal).
		SetStatus(apikey.StatusEnabled).
		Save(setupCtx)
	require.NoError(t, err)

	queryCtx := contexts.WithUser(ent.NewContext(t.Context(), client), adminUser)

	keys, err := client.APIKey.Query().
		Order(apikey.ByName()).
		All(queryCtx)
	require.NoError(t, err)

	require.Len(t, keys, 2)
	require.Equal(t, adminPersonalKey.ID, keys[0].ID)
	require.Equal(t, serviceKey.ID, keys[1].ID)
}

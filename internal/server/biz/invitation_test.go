package biz

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/hook"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/ent/userproject"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/scopes"
)

func setupInvitationService(t *testing.T) (*InvitationService, *ent.Client, context.Context) {
	t.Helper()

	client := enttest.NewEntClient(t, "sqlite3", "file:invitation?mode=memory&_fk=1")
	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
	service := &InvitationService{AbstractService: &AbstractService{db: client}}

	return service, client, ctx
}

func TestInvitationService_SingleUseInvitation(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	project, err := client.Project.Create().SetName("single-use-project").Save(ctx)
	require.NoError(t, err)

	created, err := service.CreateInvitation(contexts.WithUser(ctx, owner), project.ID, nil, 1)
	require.NoError(t, err)

	registered, err := service.RegisterInvitation(ctx, created.Token, "first@example.com", "password", "First", "Member")
	require.NoError(t, err)
	require.Equal(t, "first@example.com", registered.Email)

	_, err = service.RegisterInvitation(ctx, created.Token, "second@example.com", "password", "Second", "Member")
	require.Error(t, err)

	membership, err := client.UserProject.Query().Where(
		userproject.UserIDEQ(registered.ID),
		userproject.ProjectIDEQ(project.ID),
	).Only(ctx)
	require.NoError(t, err)
	require.Empty(t, membership.Scopes)
	require.Empty(t, registered.Scopes)
}

func TestInvitationService_UnlimitedInvitation(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	project, err := client.Project.Create().SetName("unlimited-project").Save(ctx)
	require.NoError(t, err)
	created, err := service.CreateInvitation(contexts.WithUser(ctx, owner), project.ID, nil, 0)
	require.NoError(t, err)

	first, err := service.RegisterInvitation(ctx, created.Token, "first@example.com", "password", "First", "Member")
	require.NoError(t, err)
	second, err := service.RegisterInvitation(ctx, created.Token, "second@example.com", "password", "Second", "Member")
	require.NoError(t, err)

	info, err := service.GetInvitation(ctx, created.Token)
	require.NoError(t, err)
	require.Equal(t, 0, info.MaxUses)
	require.Equal(t, 2, info.UsedCount)
	require.NotEqual(t, first.ID, second.ID)
}

func TestInvitationService_RejectsUnlimitedInvitationWithoutExpiration(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	project, err := client.Project.Create().SetName("unbounded-project").Save(ctx)
	require.NoError(t, err)
	neverExpires := 0

	_, err = service.CreateInvitation(contexts.WithUser(ctx, owner), project.ID, &neverExpires, 0)
	require.EqualError(t, err, "an unlimited invitation must have an expiration")
}

func TestInvitationService_ProjectNotFound(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)

	_, err = service.CreateInvitation(contexts.WithUser(ctx, owner), 999, nil, 1)
	require.EqualError(t, err, "project not found")
}

func TestInvitationService_ProjectPermissionDoesNotCrossProjects(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	manager, err := client.User.Create().SetEmail("manager@example.com").SetPassword("password").Save(ctx)
	require.NoError(t, err)
	allowedProject, err := client.Project.Create().SetName("allowed-project").Save(ctx)
	require.NoError(t, err)
	otherProject, err := client.Project.Create().SetName("other-project").Save(ctx)
	require.NoError(t, err)
	_, err = client.UserProject.Create().
		SetUserID(manager.ID).
		SetProjectID(allowedProject.ID).
		SetScopes([]string{string(scopes.ScopeWriteUsers)}).
		Save(ctx)
	require.NoError(t, err)
	manager, err = client.User.Query().Where(user.IDEQ(manager.ID)).WithProjectUsers().WithRoles().Only(ctx)
	require.NoError(t, err)
	managerCtx := contexts.WithUser(ctx, manager)

	_, err = service.CreateInvitation(managerCtx, otherProject.ID, nil, 1)
	require.EqualError(t, err, "permission denied: project user management access required")

	_, err = service.CreateInvitation(managerCtx, allowedProject.ID, nil, 1)
	require.NoError(t, err)
}

func TestInvitationService_ExpiredInvitation(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	project, err := client.Project.Create().SetName("expired-project").Save(ctx)
	require.NoError(t, err)
	oneHour := 1
	created, err := service.CreateInvitation(contexts.WithUser(ctx, owner), project.ID, &oneHour, 1)
	require.NoError(t, err)

	invitation, err := client.Invitation.Query().Only(ctx)
	require.NoError(t, err)
	require.NoError(t, client.Invitation.UpdateOneID(invitation.ID).SetExpiresAt(time.Now().Add(-time.Hour)).Exec(ctx))

	_, err = service.GetInvitation(ctx, created.Token)
	require.Error(t, err)
	_, err = service.RegisterInvitation(ctx, created.Token, "member@example.com", "password", "Member", "User")
	require.Error(t, err)
}

func TestInvitationService_ProjectDeletionInvalidatesInvitation(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	projectRow, err := client.Project.Create().SetName("deleted-project").Save(ctx)
	require.NoError(t, err)
	created, err := service.CreateInvitation(contexts.WithUser(ctx, owner), projectRow.ID, nil, 1)
	require.NoError(t, err)

	projectService := NewProjectService(ProjectServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})
	ownerCtx := authz.NewUserContext(ent.NewContext(context.Background(), client), owner.ID)
	ownerCtx = contexts.WithUser(ownerCtx, owner)
	require.NoError(t, projectService.DeleteProject(ownerCtx, projectRow.ID))

	_, err = service.GetInvitation(ctx, created.Token)
	require.Error(t, err)
	_, err = service.RegisterInvitation(ctx, created.Token, "member@example.com", "password", "Member", "User")
	require.Error(t, err)
	exists, err := client.User.Query().Where(user.EmailEQ("member@example.com")).Exist(ctx)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestInvitationService_ProjectDeletionWaitsForConcurrentInvitationCreation(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	projectRow, err := client.Project.Create().SetName("concurrent-delete-project").Save(ctx)
	require.NoError(t, err)
	ownerCtx := contexts.WithUser(ctx, owner)

	createEntered := make(chan struct{})
	releaseCreate := make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case releaseCreate <- struct{}{}:
		default:
		}
	})
	client.Invitation.Use(func(next ent.Mutator) ent.Mutator {
		return hook.InvitationFunc(func(ctx context.Context, mutation *ent.InvitationMutation) (ent.Value, error) {
			if mutation.Op() == ent.OpCreate {
				close(createEntered)
				<-releaseCreate
			}
			return next.Mutate(ctx, mutation)
		})
	})

	type createResult struct {
		invitation *CreatedInvitation
		err        error
	}
	created := make(chan createResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Logf("invitation creation panicked: %v", recovered)
				created <- createResult{err: fmt.Errorf("invitation creation panicked: %v", recovered)}
			}
		}()
		invitation, err := service.CreateInvitation(ownerCtx, projectRow.ID, nil, 1)
		created <- createResult{invitation: invitation, err: err}
	}()
	<-createEntered

	projectService := NewProjectService(ProjectServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})
	deleted := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Logf("project deletion panicked: %v", recovered)
				deleted <- fmt.Errorf("project deletion panicked: %v", recovered)
			}
		}()
		deleted <- projectService.DeleteProject(ownerCtx, projectRow.ID)
	}()

	select {
	case err := <-deleted:
		t.Fatalf("project deletion completed before invitation creation was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseCreate <- struct{}{}

	result := <-created
	require.NoError(t, result.err)
	require.NotNil(t, result.invitation)
	require.NoError(t, <-deleted)

	_, err = service.GetInvitation(ctx, result.invitation.Token)
	require.Error(t, err)
}

func TestInvitationService_ProjectDeletionWaitsForConcurrentInvitationRegistration(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	projectRow, err := client.Project.Create().SetName("concurrent-register-project").Save(ctx)
	require.NoError(t, err)
	created, err := service.CreateInvitation(contexts.WithUser(ctx, owner), projectRow.ID, nil, 1)
	require.NoError(t, err)

	registerEntered := make(chan struct{})
	releaseRegister := make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case releaseRegister <- struct{}{}:
		default:
		}
	})
	client.Project.Use(func(next ent.Mutator) ent.Mutator {
		return hook.ProjectFunc(func(ctx context.Context, mutation *ent.ProjectMutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			if status, ok := mutation.Status(); ok && status == project.StatusActive {
				close(registerEntered)
				<-releaseRegister
			}
			return value, err
		})
	})

	type registerResult struct {
		user *ent.User
		err  error
	}
	registered := make(chan registerResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				registered <- registerResult{err: fmt.Errorf("invitation registration panicked: %v", recovered)}
			}
		}()
		registeredUser, err := service.RegisterInvitation(ctx, created.Token, "member@example.com", "password", "Member", "User")
		registered <- registerResult{user: registeredUser, err: err}
	}()
	select {
	case <-registerEntered:
	case <-time.After(time.Second):
		t.Fatal("invitation registration did not serialize on the active project")
	}

	projectService := NewProjectService(ProjectServiceParams{
		CacheConfig: xcache.Config{Mode: xcache.ModeMemory},
		Ent:         client,
	})
	ownerCtx := authz.NewUserContext(ent.NewContext(context.Background(), client), owner.ID)
	ownerCtx = contexts.WithUser(ownerCtx, owner)
	deleted := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				deleted <- fmt.Errorf("project deletion panicked: %v", recovered)
			}
		}()
		deleted <- projectService.DeleteProject(ownerCtx, projectRow.ID)
	}()

	select {
	case err := <-deleted:
		t.Fatalf("project deletion completed before invitation registration was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseRegister <- struct{}{}

	result := <-registered
	require.NoError(t, result.err)
	require.NotNil(t, result.user)
	require.NoError(t, <-deleted)

	_, err = service.GetInvitation(ctx, created.Token)
	require.Error(t, err)
	membershipExists, err := client.UserProject.Query().Where(
		userproject.UserIDEQ(result.user.ID),
		userproject.ProjectIDEQ(projectRow.ID),
	).Exist(ctx)
	require.NoError(t, err)
	require.False(t, membershipExists)
}

func TestInvitationService_GetInvitationRejectsDeletedProject(t *testing.T) {
	service, client, ctx := setupInvitationService(t)
	defer client.Close()

	owner, err := client.User.Create().SetEmail("owner@example.com").SetPassword("password").SetIsOwner(true).Save(ctx)
	require.NoError(t, err)
	projectRow, err := client.Project.Create().SetName("orphaned-invitation-project").Save(ctx)
	require.NoError(t, err)
	created, err := service.CreateInvitation(contexts.WithUser(ctx, owner), projectRow.ID, nil, 1)
	require.NoError(t, err)
	require.NoError(t, client.Project.DeleteOneID(projectRow.ID).Exec(ctx))

	_, err = service.GetInvitation(ctx, created.Token)
	require.EqualError(t, err, "invitation project is no longer active")
}

func TestGenerateInvitationToken(t *testing.T) {
	token, err := generateInvitationToken()
	require.NoError(t, err)
	require.Len(t, token, 43)

	bytes, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	require.Len(t, bytes, 32)
}

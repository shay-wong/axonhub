package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

type recordingProviderQuotaInvalidator struct {
	channelIDs []int
}

func (r *recordingProviderQuotaInvalidator) InvalidateChannelQuota(_ context.Context, channelID int) error {
	r.channelIDs = append(r.channelIDs, channelID)
	return nil
}

func TestChannelService_InvalidatesProviderQuotaWhenQuotaIdentityChanges(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))
	ch, err := client.Channel.Create().
		SetType(channel.TypeOpencodeGo).
		SetName("OpenCode Go").
		SetBaseURL("https://opencode.ai/zen/go/v1").
		SetCredentials(objects.ChannelCredentials{APIKeys: []string{"key-1", "key-2"}}).
		SetDisabledAPIKeys([]objects.DisabledAPIKey{{Key: "key-2"}}).
		SetSupportedModels([]string{"opencode/go"}).
		SetDefaultTestModel("opencode/go").
		Save(ctx)
	require.NoError(t, err)

	invalidator := &recordingProviderQuotaInvalidator{}
	svc.SetChannelProviderQuotaInvalidator(invalidator)

	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{
		Credentials: &objects.ChannelCredentials{APIKeys: []string{"key-1", "key-2", "key-3"}},
	})
	require.NoError(t, err)
	require.Equal(t, []int{ch.ID}, invalidator.channelIDs)

	newName := "OpenCode Go renamed"
	_, err = svc.UpdateChannel(ctx, ch.ID, &ent.UpdateChannelInput{Name: &newName})
	require.NoError(t, err)
	require.Equal(t, []int{ch.ID}, invalidator.channelIDs)

	_, err = svc.DeleteDisabledAPIKeys(ctx, ch.ID, []string{"key-2"})
	require.NoError(t, err)
	require.Equal(t, []int{ch.ID, ch.ID}, invalidator.channelIDs)
}

package biz

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/pkg/xcache"
)

func TestSystemService_SetProxyPresetsNormalizesSaveAndDelete(t *testing.T) {
	service, client := setupTestSystemService(t, xcache.Config{Mode: xcache.ModeMemory})
	defer client.Close()
	ctx := authz.WithTestBypass(ent.NewContext(t.Context(), client))

	require.NoError(t, service.SetProxyPresets(ctx, []ProxyPreset{
		{Name: "old", URL: "http://proxy.example.com"},
		{Name: "new", URL: "http://proxy.example.com"},
		{Name: "second", URL: "http://proxy-2.example.com"},
	}))
	require.NoError(t, service.SaveProxyPreset(ctx, ProxyPreset{Name: "updated", URL: "http://proxy.example.com"}))
	require.NoError(t, service.DeleteProxyPreset(ctx, "http://proxy-2.example.com"))

	presets, err := service.ProxyPresets(ctx)
	require.NoError(t, err)
	require.Equal(t, []ProxyPreset{{Name: "updated", URL: "http://proxy.example.com"}}, presets)
}

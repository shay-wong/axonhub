// 职责: 验证逐 Key 额度采集兼容 fork 的规范化凭据和旧禁用记录。
// 关联文件:
//   internal/server/biz/provider_quota/apikey_quota.go
//   internal/objects/channel.go
// 遵循的 Rules:
//   .agents/rules/go-general.md
// 注意事项: 旧记录中的 Key 空白不能让已禁用凭据重新计入可用额度。

package provider_quota

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/objects"
)

func TestAPIKeyQuotaFork_NormalizedDisableIdentity(t *testing.T) {
	ch := &ent.Channel{
		Credentials: objects.ChannelCredentials{APIKeys: []string{"  padded-key  ", "next-key"}},
		DisabledAPIKeys: []objects.DisabledAPIKey{{
			Key:           "  padded-key  ",
			QuotaDisabled: true,
		}},
	}
	var requested []string
	quota, err := CollectAPIKeyQuotas(t.Context(), ch, "deepseek", time.Now(),
		func(_ context.Context, _ *ent.Channel, key string) (QuotaData, error) {
			requested = append(requested, key)
			return QuotaData{Status: "available", Ready: true}, nil
		})
	require.NoError(t, err)
	require.Equal(t, []string{"padded-key", "next-key"}, requested)
	require.Len(t, quota.APIKeyQuotas, 2)
	require.True(t, quota.APIKeyQuotas[0].Disabled)
	require.True(t, quota.APIKeyQuotas[0].QuotaDisabled)
	require.False(t, quota.APIKeyQuotas[1].Disabled)
}

// 职责: 验证配额备用队列保留渠道去重、模型回退和协议边界。
// 关联文件:
//
//	internal/server/orchestrator/candidates.go
//	internal/server/orchestrator/candidates_quota.go
//	internal/server/orchestrator/candidates_quota_test.go
//
// 注意事项: 备用队列不能再次尝试已进入首选队列的渠道。
package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/providerquotastatus"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/biz/provider_quota"
	"github.com/looplj/axonhub/llm"
)

func TestQuotaFallbackDoesNotRevisitPreferredChannel(t *testing.T) {
	for _, mode := range []string{"balanced", "sticky", "without_load_balancer"} {
		t.Run(mode, func(t *testing.T) {
			open := quotaRoutingCandidate(1, "")
			open.Models = []biz.ChannelModelEntry{{ActualModel: "gemini-3.8-flash"}}
			pressured := quotaRoutingCandidate(1, "")
			pressured.Models = []biz.ChannelModelEntry{{ActualModel: "claude-sonnet-4-6"}}
			candidates := []*ChannelModelsCandidate{open, quotaRoutingCandidate(2, ""), pressured, quotaRoutingCandidate(3, "")}
			statuses := map[int]*biz.QuotaChannelStatus{
				1: {
					Status: providerquotastatus.StatusAvailable,
					Limits: []provider_quota.QuotaLimitStatus{
						{Type: provider_quota.QuotaLimitTypeToken, Status: "exhausted", UsageRatio: 1, Window: "claude_gpt_5h", AvailabilityGroup: "antigravity_pool:claude_gpt"},
						{Type: provider_quota.QuotaLimitTypeToken, Status: "available", Window: "pay_as_you_go", AvailabilityGroup: "antigravity_pool:claude_gpt"},
						{Type: provider_quota.QuotaLimitTypeToken, Status: "available", UsageRatio: 0.1, Window: "gemini_7d", AvailabilityGroup: "antigravity_pool:gemini"},
					},
				},
				2: quotaRoutingStatus(providerquotastatus.StatusAvailable),
				3: quotaStickyOnlyStatus(),
			}
			settings := biz.QuotaRoutingSettings{DefaultMode: objects.QuotaRoutingModeBackpressure}
			balanced, gate := newQuotaRoutingSelector(candidates, statuses, settings)
			var selector CandidateSelector = balanced
			ctx := context.Background()
			want := []int{1, 2, 3}
			if mode == "sticky" {
				ctx = contexts.WithTrace(ctx, &ent.Trace{ID: 10, ThreadID: 20})
				want = []int{2, 1, 3}
			} else if mode == "without_load_balancer" {
				selector = WithQuotaRoutingSelector(&staticChannelSelector{candidates: candidates}, gate)
			}

			got, err := selector.Select(ctx, &llm.Request{Model: "requested-model"})
			require.NoError(t, err)
			require.Equal(t, want, quotaRoutingIDs(got))
			// Pressured models cannot jump ahead of other open channels.
			for _, candidate := range got {
				if candidate.Channel.ID == 1 {
					require.Equal(t, []biz.ChannelModelEntry{{ActualModel: "gemini-3.8-flash"}}, candidate.Models)
				}
			}
		})
	}
}

func TestQuotaFallbackRetainsModelsAfterChannelLimit(t *testing.T) {
	primary := quotaRoutingCandidate(1, "")
	primary.APIFormat = string(llm.APIFormatOpenAIResponse)
	primary.Models = []biz.ChannelModelEntry{{ActualModel: "primary"}}
	primary.modelAPIFormats = []string{primary.APIFormat}
	fallback := quotaRoutingCandidate(1, "")
	fallback.Priority = 10
	fallback.APIFormat = primary.APIFormat
	fallback.Models = []biz.ChannelModelEntry{{ActualModel: "fallback"}}
	fallback.modelAPIFormats = []string{fallback.APIFormat}
	otherFormat := quotaRoutingCandidate(1, "")
	otherFormat.Priority = 20
	otherFormat.APIFormat = string(llm.APIFormatOpenAIChatCompletion)
	otherFormat.Models = []biz.ChannelModelEntry{{ActualModel: "other-format"}}

	got := quotaFallbackCandidates([]*ChannelModelsCandidate{
		quotaRoutingCandidate(9, ""), primary, quotaRoutingCandidate(2, ""), fallback, otherFormat,
	}, []*ChannelModelsCandidate{quotaRoutingCandidate(9, "")}, 1)

	require.Equal(t, []int{1}, quotaRoutingIDs(got))
	require.Equal(t, []biz.ChannelModelEntry{{ActualModel: "primary"}, {ActualModel: "fallback"}}, got[0].Models)
	require.Equal(t, []string{primary.APIFormat, fallback.APIFormat}, got[0].modelAPIFormats)
	require.Equal(t, []biz.ChannelModelEntry{{ActualModel: "primary"}}, primary.Models)
	require.Equal(t, []string{primary.APIFormat}, primary.modelAPIFormats)
}

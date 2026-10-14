package orchestrator

import (
	"context"
	"encoding/json"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	responsestransformer "github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// Automatic identity preservation must retain cost injection without changing explicit pass-through.
func newResponsesCostInjector(ctx context.Context, outbound *PersistentOutboundTransformer, systemService *biz.SystemService) func([]byte) []byte {
	if outbound.isPassThroughEnabled(ctx, systemService) ||
		(!responsesIdentityPreservationEnabled(outbound) && !responsesTransportAdaptationEnabled(ctx, outbound, systemService)) {
		return nil
	}
	state := outbound.state
	if state.UsageLogService == nil || state.RequestExec == nil || systemService == nil {
		return nil
	}
	enabled, err := systemService.InjectUsageCostEnabled(ctx)
	if err != nil {
		log.Warn(ctx, "failed to get inject usage cost setting", log.Cause(err))
		return nil
	}
	if !enabled {
		return nil
	}

	// The transformed stream mutates state concurrently; keep this consumer's own tier history.
	service := state.UsageLogService
	channelID, modelID := state.RequestExec.ChannelID, state.RequestExec.ModelID
	requested, override, policy := state.RequestedServiceTier, state.RequestPricingOverride, state.RequestPricingOverridePolicy
	applied := ""
	return func(body []byte) []byte {
		prefix := ""
		if gjson.GetBytes(body, "response").IsObject() {
			prefix = "response."
		}
		if tier := gjson.GetBytes(body, prefix+"service_tier").String(); tier != "" {
			applied = tier
		}
		rawUsage := gjson.GetBytes(body, prefix+"usage")
		if !rawUsage.IsObject() {
			return body
		}
		var providerUsage responsestransformer.Usage
		if err := json.Unmarshal([]byte(rawUsage.Raw), &providerUsage); err != nil {
			log.Warn(ctx, "failed to decode Responses usage for cost injection", log.Cause(err))
			return body
		}
		usage := providerUsage.ToUsage()
		service.InjectUsageCost(ctx, channelID, modelID, usage, requested, applied, override, policy)
		var updated []byte
		var err error
		if usage.Cost == nil {
			updated, err = sjson.DeleteBytes(body, prefix+"usage.cost")
		} else {
			updated, err = sjson.SetBytes(body, prefix+"usage.cost", *usage.Cost)
		}
		if err != nil {
			log.Warn(ctx, "failed to inject Responses usage cost", log.Cause(err))
			return body
		}
		return updated
	}
}

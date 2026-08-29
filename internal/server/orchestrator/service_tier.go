package orchestrator

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

func captureRequestedServiceTier(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("capture-requested-service-tier", func(_ context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		if outbound != nil && outbound.state != nil {
			pricingOverride := requestPricingOverrideFromHTTPRequest(request, currentChannelType(outbound))
			outbound.state.RequestedServiceTier = llm.CanonicalServiceTier(serviceTierFromHTTPRequest(request))
			outbound.state.RequestPricingOverride = pricingOverride.ServiceTier
			outbound.state.RequestPricingOverridePolicy = pricingOverride.Policy
			outbound.state.SpeedMode = speedModeFromHTTPRequest(request)
			outbound.state.AppliedServiceTier = ""
			outbound.state.UsageLogEligible = false
		}
		return request, nil
	})
}

func serviceTierFromHTTPRequest(request *httpclient.Request) string {
	if request == nil || !isOpenAIServiceTierFormat(request.APIFormat) {
		return ""
	}

	if len(request.Body) > 0 && json.Valid(request.Body) {
		serviceTier, _ := serviceTierFromJSONBody(request.Body)
		return serviceTier
	}

	serviceTier, _ := serviceTierFromJSONBody(request.JSONBody)
	return serviceTier
}

type requestPricingOverride struct {
	ServiceTier string
	Policy      biz.RequestPricingOverridePolicy
}

func requestPricingOverrideFromHTTPRequest(request *httpclient.Request, channelType channel.Type) requestPricingOverride {
	if request == nil {
		return requestPricingOverride{}
	}

	if len(request.Body) > 0 && json.Valid(request.Body) {
		return requestPricingOverrideFromJSONBody(request.Body, request.APIFormat, channelType)
	}

	return requestPricingOverrideFromJSONBody(request.JSONBody, request.APIFormat, channelType)
}

func requestPricingOverrideFromJSONBody(body []byte, apiFormat string, channelType channel.Type) requestPricingOverride {
	if isAnthropicSpeedFormat(apiFormat) && fastSpeedFromJSONBody(body) {
		// Reuse the existing priority price bucket as the internal Fast billing
		// profile. Anthropic request semantics remain speed=fast, not priority.
		return requestPricingOverride{
			ServiceTier: llm.ServiceTierPriority,
			Policy:      biz.RequestPricingOverrideAlways,
		}
	}

	if channelType == channel.TypeCodex && isOpenAIServiceTierFormat(apiFormat) {
		serviceTier, ok := serviceTierFromJSONBody(body)
		serviceTier = llm.CanonicalServiceTier(serviceTier)
		if ok && (serviceTier == llm.ServiceTierPriority || serviceTier == llm.ServiceTierUltrafast) {
			return requestPricingOverride{
				ServiceTier: serviceTier,
				Policy:      biz.RequestPricingOverrideWhenAppliedDefault,
			}
		}
	}

	return requestPricingOverride{}
}

func speedModeFromHTTPRequest(request *httpclient.Request) string {
	if request == nil {
		return ""
	}

	if len(request.Body) > 0 && json.Valid(request.Body) {
		return speedModeFromJSONBody(request.Body, request.APIFormat)
	}

	return speedModeFromJSONBody(request.JSONBody, request.APIFormat)
}

func speedModeFromJSONBody(body []byte, apiFormat string) string {
	if isAnthropicSpeedFormat(apiFormat) && fastSpeedFromJSONBody(body) {
		return "fast"
	}

	if isOpenAIServiceTierFormat(apiFormat) {
		serviceTier, ok := serviceTierFromJSONBody(body)
		if ok {
			switch llm.CanonicalServiceTier(serviceTier) {
			case llm.ServiceTierPriority:
				return "fast"
			case llm.ServiceTierUltrafast:
				return "ultrafast"
			}
		}
	}

	return ""
}

func fastSpeedFromJSONBody(body []byte) bool {
	speed := gjson.GetBytes(body, "speed")

	return speed.Type == gjson.String && strings.EqualFold(strings.TrimSpace(speed.String()), "fast")
}

func isOpenAIServiceTierFormat(apiFormat string) bool {
	switch llm.APIFormat(apiFormat) {
	case llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAICompletion,
		llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseCompact:
		return true
	default:
		return false
	}
}

func isAnthropicSpeedFormat(apiFormat string) bool {
	return llm.APIFormat(apiFormat) == llm.APIFormatAnthropicMessage
}

func currentChannelType(outbound *PersistentOutboundTransformer) channel.Type {
	if outbound == nil {
		return ""
	}

	currentChannel := outbound.GetCurrentChannel()
	if currentChannel == nil || currentChannel.Channel == nil {
		return ""
	}

	return currentChannel.Type
}

func serviceTierFromJSONBody(body []byte) (string, bool) {
	serviceTier := gjson.GetBytes(body, "service_tier")
	if serviceTier.Type != gjson.String {
		return "", false
	}

	return serviceTier.String(), true
}

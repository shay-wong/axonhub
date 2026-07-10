package orchestrator

import (
	"context"
	"encoding/json"

	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
)

func captureRequestedServiceTier(outbound *PersistentOutboundTransformer) pipeline.Middleware {
	return pipeline.OnRawRequest("capture-requested-service-tier", func(_ context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		if outbound != nil && outbound.state != nil {
			outbound.state.RequestedServiceTier = llm.CanonicalServiceTier(serviceTierFromHTTPRequest(request))
			outbound.state.AppliedServiceTier = ""
			outbound.state.UsageLogEligible = false
		}
		return request, nil
	})
}

func serviceTierFromHTTPRequest(request *httpclient.Request) string {
	if request == nil {
		return ""
	}

	if len(request.Body) > 0 && json.Valid(request.Body) {
		serviceTier, _ := serviceTierFromJSONBody(request.Body)
		return serviceTier
	}

	serviceTier, _ := serviceTierFromJSONBody(request.JSONBody)
	return serviceTier
}

func serviceTierFromJSONBody(body []byte) (string, bool) {
	serviceTier := gjson.GetBytes(body, "service_tier")
	if serviceTier.Type != gjson.String {
		return "", false
	}

	return serviceTier.String(), true
}

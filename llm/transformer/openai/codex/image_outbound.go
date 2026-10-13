package codex

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/oauth"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

type ImageParams struct {
	TokenProvider oauth.TokenGetter
	BaseURL       string
	EndpointPath  string
}

type imageOutbound struct {
	transformer.Outbound
	tokens oauth.TokenGetter
}

func (t *imageOutbound) FinalizeTransportRequest(request *httpclient.Request) *httpclient.Request {
	normalizeCodexSessionHeaders(request)
	return request
}

func (t *imageOutbound) AllowPassThroughBody(_ context.Context, req *llm.Request, _ *httpclient.Request) bool {
	return req.APIFormat != llm.APIFormatOpenAIImageEdit
}

func NewImageOutboundTransformer(params ImageParams) (transformer.Outbound, error) {
	if params.TokenProvider == nil {
		return nil, fmt.Errorf("token provider is required")
	}
	baseURL := params.BaseURL
	if baseURL == "" || baseURL == "https://api.openai.com/v1" {
		baseURL = codexBaseURL
	}
	baseURL = strings.Replace(strings.Replace(baseURL, "wss://", "https://", 1), "ws://", "http://", 1)
	out, err := openai.NewOutboundTransformerWithConfig(&openai.Config{
		PlatformType: openai.PlatformOpenAI,
		BaseURL:      baseURL, EndpointPath: params.EndpointPath, ImageEditJSON: true,
		APIKeyProvider: auth.NewStaticKeyProvider(""),
	})
	if err != nil {
		return nil, err
	}
	return &imageOutbound{Outbound: out, tokens: params.TokenProvider}, nil
}

func (t *imageOutbound) TransformRequest(ctx context.Context, req *llm.Request) (*httpclient.Request, error) {
	if req == nil || req.RequestType != llm.RequestTypeImage {
		return nil, fmt.Errorf("%w: native Images endpoint requires an image request", transformer.ErrInvalidRequest)
	}
	if req.Stream != nil && *req.Stream {
		return nil, fmt.Errorf("%w: image request does not support streaming", transformer.ErrInvalidRequest)
	}
	creds, err := t.tokens.Get(ctx)
	if err != nil {
		return nil, err
	}
	hreq, err := t.Outbound.TransformRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	hreq.Auth = &httpclient.AuthConfig{Type: httpclient.AuthTypeBearer, APIKey: creds.AccessToken}
	var raw http.Header
	if req.RawRequest != nil {
		raw = req.RawRequest.Headers
	}
	hreq.SkipInboundHeaderMerge = skipClientFingerprintHeaders(raw, hreq.SkipInboundHeaderMerge)
	hreq.SkipInboundHeaderMerge[http.CanonicalHeaderKey(SessionHeader)] = true
	hreq.SkipInboundHeaderMerge[http.CanonicalHeaderKey(SessionHeaderHyphen)] = true
	// Client UA forwarding belongs to the orchestrator's opt-in middleware.
	hreq.Headers.Set("User-Agent", codexDefaultUserAgent)
	hreq.Headers.Set("Originator", AxonHubOriginator)
	for _, header := range append([]string{"Originator"}, PassthroughHeaders...) {
		if value := raw.Get(header); value != "" {
			hreq.Headers.Set(header, value)
		}
	}
	session := ensureSessionID(ctx, GetSessionIDFromHeaders(raw), raw.Get(TurnMetadataHeader))
	if hreq.TransformerMetadata == nil {
		hreq.TransformerMetadata = map[string]any{}
	}
	hreq.TransformerMetadata[codexSessionIDMetadataKey] = session
	normalizeCodexSessionHeaders(hreq)
	if accountID := ExtractChatGPTAccountIDFromJWT(creds.AccessToken); accountID != "" {
		hreq.Headers.Set("Chatgpt-Account-Id", accountID)
	}
	return hreq, nil
}

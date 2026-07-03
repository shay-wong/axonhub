package pipeline

import (
	"context"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func (p *pipeline) transformHTTPError(ctx context.Context, httpErr *httpclient.Error) *llm.ResponseError {
	respErr := p.Outbound.TransformError(ctx, httpErr)
	if respErr != nil && httpErr != nil && httpErr.Truncated {
		respErr.Detail.Truncated = true
	}

	return respErr
}

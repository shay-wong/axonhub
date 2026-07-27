package responses

import (
	"fmt"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/internal/pkg/xurl"
)

func BuildImageResponse(upstream *Response, metadata map[string]any) (*llm.Response, error) {
	if upstream == nil {
		return nil, fmt.Errorf("response is nil")
	}

	if upstream.Error != nil {
		details := make([]string, 0, 2)
		if errorType := strings.TrimSpace(upstream.Error.Type); errorType != "" {
			details = append(details, "type="+errorType)
		}
		if code := strings.TrimSpace(upstream.Error.Code); code != "" {
			details = append(details, "code="+code)
		}
		if len(details) == 0 {
			return nil, fmt.Errorf("codex image response failed")
		}

		return nil, fmt.Errorf("codex image response failed (%s)", strings.Join(details, ", "))
	}

	format, _ := metadata["codex_image_output_format"].(string)
	if format == "" {
		format = "png"
	}

	imageResp := &llm.ImageResponse{
		Created:      upstream.CreatedAt,
		Data:         make([]llm.ImageData, 0),
		OutputFormat: format,
	}

	if quality, ok := metadata["codex_image_quality"].(string); ok {
		imageResp.Quality = quality
	}
	if size, ok := metadata["codex_image_size"].(string); ok {
		imageResp.Size = size
	}
	if background, ok := metadata["codex_image_background"].(string); ok {
		imageResp.Background = background
	}

	outputTypes := make([]string, 0, len(upstream.Output))
	hasImageGenerationCall := false
	for _, item := range upstream.Output {
		outputType := strings.TrimSpace(item.Type)
		if outputType == "" {
			outputType = "unknown"
		}
		outputTypes = append(outputTypes, outputType)

		if item.Type != "image_generation_call" {
			continue
		}
		hasImageGenerationCall = true
		if item.Result == nil || *item.Result == "" {
			continue
		}

		b64JSON := xurl.ExtractBase64FromDataURL(*item.Result)
		imageResp.Data = append(imageResp.Data, llm.ImageData{
			B64JSON: b64JSON,
		})
	}

	if len(imageResp.Data) == 0 {
		return nil, imageResponseNoResultError(upstream, outputTypes, hasImageGenerationCall)
	}

	result := &llm.Response{
		ID:          upstream.ID,
		Object:      "image.generation",
		Created:     upstream.CreatedAt,
		Model:       upstream.Model,
		RequestType: llm.RequestTypeImage,
		Image:       imageResp,
	}

	if upstream.Usage != nil {
		result.Usage = upstream.Usage.ToUsage()
	}

	if model, ok := metadata["codex_image_model"].(string); ok && model != "" {
		result.Model = model
	}

	return result, nil
}

func imageResponseNoResultError(upstream *Response, outputTypes []string, hasImageGenerationCall bool) error {
	status := "unknown"
	if upstream.Status != nil && strings.TrimSpace(*upstream.Status) != "" {
		status = strings.TrimSpace(*upstream.Status)
	}

	details := "status=" + status
	if len(outputTypes) > 0 {
		details += ", output_types=" + strings.Join(outputTypes, ",")
	}

	if upstream.IncompleteDetails != nil && strings.TrimSpace(upstream.IncompleteDetails.Reason) != "" {
		return fmt.Errorf("codex image response incomplete: %s (%s)", strings.TrimSpace(upstream.IncompleteDetails.Reason), details)
	}
	if hasImageGenerationCall {
		return fmt.Errorf("codex image response contained image_generation_call without result (%s)", details)
	}
	if len(outputTypes) == 0 {
		return fmt.Errorf("codex image response did not produce any output (%s)", details)
	}

	return fmt.Errorf("codex image response did not produce an image (%s)", details)
}

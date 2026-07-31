package biz

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/looplj/axonhub/internal/ent"
)

const (
	// SystemKeyProxyPresets is the key used to store proxy preset configurations.
	// The value is JSON-encoded []ProxyPreset.
	SystemKeyProxyPresets = "system_proxy_presets"
)

// ProxyPreset represents a proxy configuration preset.
type ProxyPreset struct {
	Name     string `json:"name,omitempty"`
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// ProxyPresets retrieves all proxy presets.
func (s *SystemService) ProxyPresets(ctx context.Context) ([]ProxyPreset, error) {
	value, err := s.getSystemValue(ctx, SystemKeyProxyPresets)
	if err != nil {
		if ent.IsNotFound(err) {
			return []ProxyPreset{}, nil
		}

		return nil, fmt.Errorf("failed to get proxy presets: %w", err)
	}

	var presets []ProxyPreset
	if err := json.Unmarshal([]byte(value), &presets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal proxy presets: %w", err)
	}

	return presets, nil
}

// SaveProxyPreset adds or updates a proxy preset, deduplicating by URL.
func (s *SystemService) SaveProxyPreset(ctx context.Context, preset ProxyPreset) error {
	presets, err := s.ProxyPresets(ctx)
	if err != nil {
		return err
	}

	return s.SetProxyPresets(ctx, append(presets, preset))
}

// DeleteProxyPreset removes a proxy preset by URL.
func (s *SystemService) DeleteProxyPreset(ctx context.Context, url string) error {
	presets, err := s.ProxyPresets(ctx)
	if err != nil {
		return err
	}

	filtered := make([]ProxyPreset, 0, len(presets))
	for _, p := range presets {
		if p.URL != url {
			filtered = append(filtered, p)
		}
	}

	return s.SetProxyPresets(ctx, filtered)
}

// SetProxyPresets replaces all proxy presets, keeping the last preset for each URL.
func (s *SystemService) SetProxyPresets(ctx context.Context, presets []ProxyPreset) error {
	normalized := make([]ProxyPreset, 0, len(presets))
	indices := make(map[string]int, len(presets))
	for _, preset := range presets {
		if index, ok := indices[preset.URL]; ok {
			normalized[index] = preset
			continue
		}
		indices[preset.URL] = len(normalized)
		normalized = append(normalized, preset)
	}

	jsonBytes, err := json.Marshal(normalized) //nolint:gosec // G117: Password field is stored internally, not exposed to API responses
	if err != nil {
		return fmt.Errorf("failed to marshal proxy presets: %w", err)
	}

	return s.setSystemValue(ctx, SystemKeyProxyPresets, string(jsonBytes))
}

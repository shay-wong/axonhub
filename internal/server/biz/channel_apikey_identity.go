package biz

import (
	"strings"

	"github.com/looplj/axonhub/internal/log"
)

const apiKeyIdentityMinLength = 8

// ChannelAPIKeyIdentity is the non-sensitive identity of an upstream API key.
type ChannelAPIKeyIdentity struct {
	Name   string `json:"name,omitempty"`
	Suffix string `json:"suffix,omitempty"`
}

func (c *Channel) APIKeyIdentity(apiKey string) ChannelAPIKeyIdentity {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ChannelAPIKeyIdentity{}
	}

	identity := ChannelAPIKeyIdentity{}
	if c != nil {
		for _, config := range c.apiKeyConfigs() {
			if config.Key == apiKey {
				identity.Name = strings.TrimSpace(config.Name)
				break
			}
		}
	}

	if len(apiKey) > apiKeyIdentityMinLength {
		identity.Suffix = apiKey[len(apiKey)-4:]
	}

	return identity
}

func (c *Channel) APIKeyIdentities(apiKeys []string) []ChannelAPIKeyIdentity {
	identities := make([]ChannelAPIKeyIdentity, 0, len(apiKeys))
	for _, apiKey := range apiKeys {
		identity := c.APIKeyIdentity(apiKey)
		if identity.Name == "" && identity.Suffix == "" {
			continue
		}
		identities = append(identities, identity)
	}

	return identities
}

func apiKeyIdentityLogFields(identity ChannelAPIKeyIdentity) []log.Field {
	fields := make([]log.Field, 0, 2)
	if identity.Name != "" {
		fields = append(fields, log.String("api_key_name", identity.Name))
	}
	if identity.Suffix != "" {
		fields = append(fields, log.String("api_key_suffix", identity.Suffix))
	}

	return fields
}

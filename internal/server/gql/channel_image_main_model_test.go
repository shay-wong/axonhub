package gql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemovedImageMainModelFieldsNotInSchema(t *testing.T) {
	schema := NewExecutableSchema(Config{Resolvers: &Resolver{}}).Schema()
	for _, name := range []string{
		"ChannelSettings", "ChannelSettingsInput", "UpdateChannelSettingsInput",
		"SystemModelSettings", "UpdateSystemModelSettingsInput",
	} {
		t.Run(name, func(t *testing.T) {
			definition := schema.Types[name]
			require.NotNil(t, definition)
			require.Nil(t, definition.Fields.ForName("codexImageMainModel"))
		})
	}
}

func TestRemovedAPIKeySelectionFieldNotInSchema(t *testing.T) {
	schema := NewExecutableSchema(Config{Resolvers: &Resolver{}}).Schema()
	for _, name := range []string{"ChannelSettings", "ChannelSettingsInput", "UpdateChannelSettingsInput"} {
		t.Run(name, func(t *testing.T) {
			definition := schema.Types[name]
			require.NotNil(t, definition)
			require.Nil(t, definition.Fields.ForName("apiKeySelectionStrategy"))
			require.NotNil(t, definition.Fields.ForName("apiKeyStrategy"))
		})
	}
}

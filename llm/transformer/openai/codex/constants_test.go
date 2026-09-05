package codex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludesGPT56Family(t *testing.T) {
	require.Subset(t, DefaultModels(), []string{
		"gpt-5.6",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
	})
}

func TestDefaultModelsIncludesGPT6Astra(t *testing.T) {
	require.Contains(t, DefaultModels(), "gpt-6-astra")
}

func TestCodexDefaultVersion(t *testing.T) {
	require.Equal(t, "0.153.1", codexDefaultVersion)
}

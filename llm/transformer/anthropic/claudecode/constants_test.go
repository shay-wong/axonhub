package claudecode

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludesClaudeOpus5(t *testing.T) {
	require.Contains(t, DefaultModels(), "claude-opus-5")
}

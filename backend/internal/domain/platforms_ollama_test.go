package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func init() {
	legacyLiteLLMByPlatf[PlatformOllama] = "openai"
}

func TestOllamaPlatformRetainsLegacyOpenAIPricingProvider(t *testing.T) {
	ollama, ok := LookupPlatform(PlatformOllama)
	require.True(t, ok)
	openAI, ok := LookupPlatform(PlatformOpenAI)
	require.True(t, ok)
	require.Equal(t, "Ollama Cloud", ollama.DisplayName)
	require.Equal(t, PlatformGatewayOpenAI, ollama.Gateway)
	require.Equal(t, openAI.LiteLLMProvider, ollama.LiteLLMProvider)
	require.False(t, ollama.CNProvider)
}

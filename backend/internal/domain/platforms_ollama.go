package domain

// PlatformOllama identifies Ollama Cloud API key accounts.
const PlatformOllama = "ollama"

func init() {
	if IsConcretePlatform(PlatformOllama) {
		return
	}
	platformList = append(platformList, PlatformSpec{
		ID:          PlatformOllama,
		DisplayName: "Ollama Cloud",
		Gateway:     PlatformGatewayOpenAI,
		// Keep the catalog selection used by migrated OpenAI API key accounts.
		LiteLLMProvider: "openai",
	})
	platformIndex[PlatformOllama] = len(platformList) - 1
}

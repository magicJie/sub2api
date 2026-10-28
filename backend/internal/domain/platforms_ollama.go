package domain

// PlatformOllama identifies Ollama Cloud API key accounts.
const PlatformOllama = "ollama"

func init() {
	if IsConcretePlatform(PlatformOllama) {
		return
	}
	spec := PlatformSpec{
		ID:          PlatformOllama,
		DisplayName: "Ollama Cloud",
		Gateway:     PlatformGatewayOpenAI,
		// Keep the catalog selection used by migrated OpenAI API key accounts.
		LiteLLMProvider: "openai",
	}
	// 展示顺序：排在 OpenCode 之前。
	at := len(platformList)
	if i, ok := platformIndex[PlatformOpenCodeGo]; ok {
		at = i
	}
	platformList = append(platformList[:at], append([]PlatformSpec{spec}, platformList[at:]...)...)
	for i, p := range platformList {
		platformIndex[p.ID] = i
	}
}

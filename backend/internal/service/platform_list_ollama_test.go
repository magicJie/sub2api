//go:build unit

package service

import "slices"

// Ollama 登记在 OpenCode 之前（展示顺序）。
func init() {
	legacyAllPlatforms = insertBeforePlatform(legacyAllPlatforms, PlatformOllama, PlatformOpenCodeGo)
	legacySchedulerSnapshotPlatforms = insertBeforePlatform(legacySchedulerSnapshotPlatforms, PlatformOllama, PlatformOpenCodeGo)
	legacyCompositeMatchingPlatforms = legacySchedulerSnapshotPlatforms
}

func insertBeforePlatform(list []string, platform, before string) []string {
	if i := slices.Index(list, before); i >= 0 {
		return slices.Insert(slices.Clone(list), i, platform)
	}
	return append(list, platform)
}

//go:build unit

package service

func init() {
	legacyAllPlatforms = append(legacyAllPlatforms, PlatformOllama)
	legacySchedulerSnapshotPlatforms = append(legacySchedulerSnapshotPlatforms, PlatformOllama)
	legacyCompositeMatchingPlatforms = legacySchedulerSnapshotPlatforms
}

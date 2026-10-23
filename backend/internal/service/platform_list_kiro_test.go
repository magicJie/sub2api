//go:build unit

package service

// 把 fork 登记的 Kiro 补进上游平台清单测试的基准列表，避免修改上游测试文件。
func init() {
	legacyAllPlatforms = append(legacyAllPlatforms, PlatformKiro)
	legacySchedulerSnapshotPlatforms = append(legacySchedulerSnapshotPlatforms, PlatformKiro)
	legacyCompositeMatchingPlatforms = legacySchedulerSnapshotPlatforms
}

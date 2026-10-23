package domain

// 把 fork 登记的 Kiro 补进上游平台清单测试的基准列表，避免修改上游测试文件。
func init() {
	legacyDisplayOrder = append(legacyDisplayOrder, PlatformKiro)
	legacyCompositePrecedence = append(legacyCompositePrecedence, PlatformKiro)
	legacyLiteLLMByPlatf[PlatformKiro] = "anthropic"
}

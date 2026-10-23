package domain

// Kiro 平台登记（fork 自维护）。独立文件 + init 追加，避免改动上游 platformList
// 字面量：上游每次新增平台都会改那几行，放在这里同步时不会产生冲突。
//
// init 在本包所有包级变量初始化之后、任何依赖包（service 等）初始化之前执行，
// 因此 service.AllowedQuotaPlatforms 等由清单派生的包级变量能看到 Kiro。

// PlatformKiro 是 Kiro（AWS CodeWhisperer / Amazon Q Developer）平台，实现见 internal/kiro。
const PlatformKiro = "kiro"

func init() {
	if IsConcretePlatform(PlatformKiro) {
		return
	}
	platformList = append(platformList, PlatformSpec{
		ID:              PlatformKiro,
		DisplayName:     "Kiro",
		Gateway:         PlatformGatewayAnthropic,
		LiteLLMProvider: "anthropic",
	})
	platformIndex[PlatformKiro] = len(platformList) - 1
}

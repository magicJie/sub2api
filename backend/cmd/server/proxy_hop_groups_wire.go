package main

// 链式代理（fork）：只在服务端组合根引入 mihomo，业务 service 使用无内核依赖的契约。
import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/hopgroup"
	"github.com/Wei-Shaw/sub2api/internal/pkg/mihomohop"
)

func provideProxyHopGroupRuntime() hopgroup.Runtime { return mihomohop.Default() }

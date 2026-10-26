package service

// 链式代理（fork）：首跳组持久化模型；订阅 URL 是凭据，不允许 JSON 序列化。

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"time"
)

var (
	ErrProxyHopGroupNotFound   = infraerrors.NotFound("PROXY_HOP_GROUP_NOT_FOUND", "first-hop group not found")
	ErrProxyHopGroupNameExists = infraerrors.Conflict("PROXY_HOP_GROUP_NAME_EXISTS", "first-hop group name already exists")
)

type ProxyHopGroup struct {
	ID                         int64     `json:"id"`
	Name                       string    `json:"name"`
	Strategy                   string    `json:"strategy"`
	TestURL                    string    `json:"test_url"`
	IntervalSeconds            int       `json:"interval_seconds"`
	ToleranceMS                int       `json:"tolerance_ms"`
	SubscriptionURLs           []string  `json:"-"`
	MemberProxyIDs             []int64   `json:"member_proxy_ids"`
	SubscriptionUpdateInterval int       `json:"subscription_update_interval"`
	Status                     string    `json:"status"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

type ProxyHopGroupRepository interface {
	List(context.Context) ([]ProxyHopGroup, error)
	Get(context.Context, int64) (*ProxyHopGroup, error)
	Create(context.Context, *ProxyHopGroup) error
	Update(context.Context, *ProxyHopGroup) error
	Delete(context.Context, int64) error
}

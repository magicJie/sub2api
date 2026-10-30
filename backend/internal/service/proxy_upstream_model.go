package service

// 链式代理（fork）：前置端点 / 前置组 / 槽位的持久化模型，见 docs/PROXY_IP_MODEL.md 第 2.2、3.4 节与迁移 257。

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrProxyUpstreamEndpointNotFound   = infraerrors.NotFound("PROXY_UPSTREAM_ENDPOINT_NOT_FOUND", "upstream endpoint not found")
	ErrProxyUpstreamEndpointNameExists = infraerrors.Conflict("PROXY_UPSTREAM_ENDPOINT_NAME_EXISTS", "upstream endpoint name already exists")
	ErrProxyUpstreamEndpointInUse      = infraerrors.Conflict("PROXY_UPSTREAM_ENDPOINT_IN_USE", "upstream endpoint is used by an upstream group slot")
	ErrProxyUpstreamGroupNotFound      = infraerrors.NotFound("PROXY_UPSTREAM_GROUP_NOT_FOUND", "upstream group not found")
	ErrProxyUpstreamGroupNameExists    = infraerrors.Conflict("PROXY_UPSTREAM_GROUP_NAME_EXISTS", "upstream group name already exists")
	ErrProxyUpstreamGroupInUse         = infraerrors.Conflict("PROXY_UPSTREAM_GROUP_IN_USE", "upstream group is used by proxies")
)

// 前置组默认参数（迁移 257 的列默认值与此一致）。
const (
	ProxyUpstreamDefaultProbeURL      = "https://www.gstatic.com/generate_204"
	ProxyUpstreamDefaultProbeInterval = 30
	ProxyUpstreamDefaultFailThreshold = 3
	ProxyUpstreamDefaultSlotWeight    = 1
	ProxyUpstreamMaxSlotWeight        = 100
	ProxyUpstreamDefaultRecover       = 2
	ProxyUpstreamDefaultFailback      = 300
)

// ProxyUpstreamEndpoint 是外部 AIO 的一个 listener（socks5 / http），不进代理池、不绑账号。
type ProxyUpstreamEndpoint struct {
	ID        int64
	Name      string
	Protocol  string
	Host      string
	Port      int
	Username  string
	Password  string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProxyUpstreamSlot 是前置组里的一条有序端点列表，第一个为主，其余为备。
type ProxyUpstreamSlot struct {
	ID           int64
	GroupID      int64
	Name         string
	Position     int
	Weight       int // 1-100，落地按加权 rendezvous hash 选槽
	EndpointIDs  []int64
	AlertEventID *int64
}

// ProxyUpstreamGroup 是槽位集合；落地按 rendezvous hash 固定到其中一个可用槽位。
type ProxyUpstreamGroup struct {
	ID                   int64
	Name                 string
	Status               string
	ProbeURL             string
	ProbeIntervalSeconds int
	FailThreshold        int
	RecoverThreshold     int
	FailbackSeconds      int
	CreatedAt            time.Time
	UpdatedAt            time.Time
	Slots                []ProxyUpstreamSlot
}

type ProxyUpstreamRepository interface {
	ListEndpoints(ctx context.Context) ([]ProxyUpstreamEndpoint, error)
	GetEndpoint(ctx context.Context, id int64) (*ProxyUpstreamEndpoint, error)
	CreateEndpoint(ctx context.Context, e *ProxyUpstreamEndpoint) error
	UpdateEndpoint(ctx context.Context, e *ProxyUpstreamEndpoint) error
	DeleteEndpoint(ctx context.Context, id int64) error

	// ListGroups 返回全部前置组（含槽位，按 position 排序）。
	ListGroups(ctx context.Context) ([]ProxyUpstreamGroup, error)
	GetGroup(ctx context.Context, id int64) (*ProxyUpstreamGroup, error)
	// CreateGroup / UpdateGroup 在一个事务里写组与槽位；UpdateGroup 按槽位 id 保留已有槽位，其余删除。
	CreateGroup(ctx context.Context, g *ProxyUpstreamGroup) error
	UpdateGroup(ctx context.Context, g *ProxyUpstreamGroup) error
	DeleteGroup(ctx context.Context, id int64) error
	SetSlotAlertEventID(ctx context.Context, slotID int64, alertEventID *int64) error
}

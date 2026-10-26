// Package hopgroup defines the engine-neutral first-hop runtime contract (fork).
// Keeping this contract separate avoids loading mihomo's process-global init state
// into service consumers that do not instantiate a proxy runtime.
package hopgroup

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxychain"
)

type Group struct {
	ID                                                       int64
	Name, Strategy, TestURL                                  string
	IntervalSeconds, ToleranceMS, SubscriptionUpdateInterval int
	SubscriptionURLs                                         []string
	Members                                                  []proxychain.Hop
}
type NodeStatus struct {
	Name    string `json:"name"`
	Alive   bool   `json:"alive"`
	DelayMS uint16 `json:"delay_ms"`
}
type ProviderStatus struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
	NodeCount int       `json:"node_count"`
	LastError string    `json:"last_error,omitempty"`
}
type GroupStatus struct {
	Selected  string           `json:"selected"`
	Nodes     []NodeStatus     `json:"nodes"`
	Providers []ProviderStatus `json:"providers"`
}

type Runtime interface {
	Replace(context.Context, []Group) error
	Endpoint(int64) (proxychain.Hop, error)
	Status(int64) (GroupStatus, error)
	Close() error
}

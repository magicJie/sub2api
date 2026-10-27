package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 代理池（fork）：平台 IP 策略（迁移 251），替代代理池上的 platform_limits，见 docs/PROXY_IP_MODEL.md。
//
//   - churn（消耗型，如 kiro）：账号用完即弃；每个落地 IP 按该平台历史累计账号数退役，优先用历史计数最少的 IP。
//   - sticky（长期型，如 opencode、ollama-cloud）：账号终身固定一个落地 IP；分配时优先已被其他 churn
//     平台用满的 IP，把新 IP 留给消耗型平台。
//   - 没有策略的平台不能从代理池分配。
//   - 计数按落地 IP（host）合并，IP 可跨平台共用；退役永久，不回池。

const (
	ProxyIPPolicyModeChurn  = "churn"
	ProxyIPPolicyModeSticky = "sticky"

	ProxyIPFailurePause    = "pause"
	ProxyIPFailureReassign = "reassign"
)

var (
	ErrProxyIPPolicyMissing      = infraerrors.BadRequest("PROXY_IP_POLICY_MISSING", "platform has no IP policy; configure one before allocating from proxy pools")
	ErrProxyIPPolicyNotFound     = infraerrors.NotFound("PROXY_IP_POLICY_NOT_FOUND", "platform IP policy not found")
	ErrProxyIPPolicyInvalid      = infraerrors.BadRequest("PROXY_IP_POLICY_INVALID", "invalid platform IP policy")
	ErrProxyIPPolicyPoolDenied   = infraerrors.BadRequest("PROXY_IP_POLICY_POOL_DENIED", "platform IP policy does not allow this proxy pool")
	ErrProxyIPPolicyPoolNotFound = infraerrors.BadRequest("PROXY_IP_POLICY_POOL_NOT_FOUND", "platform IP policy references a missing proxy pool")
)

type ProxyIPPolicy struct {
	ID       int64
	Platform string
	Mode     string
	// PoolIDs 为空表示全部代理池。
	PoolIDs []int64
	// MaxLifetimeAccounts 每个落地 IP 对该平台的历史累计账号上限，nil 不限。
	MaxLifetimeAccounts *int
	// MaxActiveAccounts 每个落地 IP 对该平台同时在用的账号上限，nil 不限。
	MaxActiveAccounts *int
	OnEgressFailure   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// AllowsPool 报告策略是否允许从该代理池分配。
func (p *ProxyIPPolicy) AllowsPool(poolID int64) bool {
	if len(p.PoolIDs) == 0 {
		return true
	}
	for _, id := range p.PoolIDs {
		if id == poolID {
			return true
		}
	}
	return false
}

// retired 报告该落地 IP 是否已对策略所属平台退役（历史计数达到上限）。
func (p *ProxyIPPolicy) retired(m *ProxyPoolMember) bool {
	return p.MaxLifetimeAccounts != nil && m.HistoryCounts[p.Platform] >= int64(*p.MaxLifetimeAccounts)
}

// full 报告该落地 IP 对策略所属平台的在用数是否已满。
func (p *ProxyIPPolicy) full(m *ProxyPoolMember) bool {
	return p.MaxActiveAccounts != nil && m.ActiveCounts[p.Platform] >= int64(*p.MaxActiveAccounts)
}

func normalizeProxyIPPolicy(p *ProxyIPPolicy) error {
	p.Platform = strings.TrimSpace(p.Platform)
	if p.Platform == "" {
		return ErrProxyIPPolicyInvalid
	}
	switch p.Mode {
	case ProxyIPPolicyModeChurn, ProxyIPPolicyModeSticky:
	default:
		return ErrProxyIPPolicyInvalid
	}
	if (p.MaxLifetimeAccounts != nil && *p.MaxLifetimeAccounts < 1) || (p.MaxActiveAccounts != nil && *p.MaxActiveAccounts < 1) {
		return ErrProxyPoolLimitInvalid
	}
	switch {
	case p.Mode == ProxyIPPolicyModeSticky:
		// 长期账号换 IP 本身就是风控信号：故障只暂停，由人工处理。
		p.OnEgressFailure = ProxyIPFailurePause
	case p.OnEgressFailure == "":
		p.OnEgressFailure = ProxyIPFailureReassign
	case p.OnEgressFailure != ProxyIPFailurePause && p.OnEgressFailure != ProxyIPFailureReassign:
		return ErrProxyIPPolicyInvalid
	}
	seen := make(map[int64]bool, len(p.PoolIDs))
	ids := make([]int64, 0, len(p.PoolIDs))
	for _, id := range p.PoolIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	p.PoolIDs = ids
	return nil
}

func (s *ProxyPoolService) ListPolicies(ctx context.Context) ([]ProxyIPPolicy, error) {
	return s.repo.ListPolicies(ctx)
}

// UpsertPolicy 新建或覆盖某平台的 IP 策略。
func (s *ProxyPoolService) UpsertPolicy(ctx context.Context, p *ProxyIPPolicy) (*ProxyIPPolicy, error) {
	if err := normalizeProxyIPPolicy(p); err != nil {
		return nil, err
	}
	for _, id := range p.PoolIDs {
		if _, err := s.repo.GetPool(ctx, id); err != nil {
			if errors.Is(err, ErrProxyPoolNotFound) {
				return nil, ErrProxyIPPolicyPoolNotFound
			}
			return nil, err
		}
	}
	if err := s.repo.UpsertPolicy(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// DeletePolicy 删除平台策略；之后该平台不能再从代理池分配（已有绑定不受影响）。
func (s *ProxyPoolService) DeletePolicy(ctx context.Context, platform string) error {
	return s.repo.DeletePolicy(ctx, strings.TrimSpace(platform))
}

// policyFor 返回平台策略，缺失时返回 ErrProxyIPPolicyMissing。
func policyFor(policies []ProxyIPPolicy, platform string) (*ProxyIPPolicy, error) {
	for i := range policies {
		if policies[i].Platform == platform {
			return &policies[i], nil
		}
	}
	return nil, ErrProxyIPPolicyMissing
}

// ProxyPoolRetiredPlatforms 返回该落地 IP 已退役（达到历史上限）的平台。
func ProxyPoolRetiredPlatforms(policies []ProxyIPPolicy, m *ProxyPoolMember) []string {
	out := make([]string, 0)
	for i := range policies {
		if policies[i].retired(m) {
			out = append(out, policies[i].Platform)
		}
	}
	sort.Strings(out)
	return out
}

func proxyPoolMemberAvailable(policy *ProxyIPPolicy, m *ProxyPoolMember, now time.Time) bool {
	if !m.Proxy.IsActive() || m.Proxy.IsExpired(now) {
		return false
	}
	return !policy.retired(m) && !policy.full(m)
}

// usedUpByChurn 报告该落地 IP 是否已被除 platform 外的某个 churn 平台用满（可转给长期平台继续用）。
func usedUpByChurn(policies []ProxyIPPolicy, platform string, m *ProxyPoolMember) bool {
	for i := range policies {
		p := &policies[i]
		if p.Platform != platform && p.Mode == ProxyIPPolicyModeChurn && p.retired(m) {
			return true
		}
	}
	return false
}

func totalActive(m *ProxyPoolMember) int64 {
	var n int64
	for _, c := range m.ActiveCounts {
		n += c
	}
	return n
}

// sortProxyPoolCandidates 按策略排序候选：
// churn 取该平台历史计数最少的，尽量把每个 IP 的额度均匀用满；
// sticky 先取已被其他 churn 平台用满的 IP，再取总在用账号最少的，把新 IP 留给消耗型平台。
func sortProxyPoolCandidates(policies []ProxyIPPolicy, policy *ProxyIPPolicy, candidates []ProxyPoolMember) {
	platform := policy.Platform
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := &candidates[i], &candidates[j]
		if policy.Mode == ProxyIPPolicyModeSticky {
			ua, ub := usedUpByChurn(policies, platform, a), usedUpByChurn(policies, platform, b)
			if ua != ub {
				return ua
			}
			if ta, tb := totalActive(a), totalActive(b); ta != tb {
				return ta < tb
			}
		}
		if ca, cb := a.HistoryCounts[platform], b.HistoryCounts[platform]; ca != cb {
			return ca < cb
		}
		return a.Proxy.ID < b.Proxy.ID
	})
}

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
//   - 分组策略（迁移 260）按分组覆盖平台策略：同平台不同类型的账号（如 openai plus 消耗型、pro 长期型）
//     放在不同分组各配一条。账号所在分组有同平台的分组策略时优先使用，否则用平台策略。
//     计数口径不变，仍按落地 IP + 平台统计（上游看到的是同一平台在该 IP 上的全部账号）。

const (
	ProxyIPPolicyModeChurn  = "churn"
	ProxyIPPolicyModeSticky = "sticky"

	ProxyIPFailurePause    = "pause"
	ProxyIPFailureReassign = "reassign"
)

var (
	ErrProxyIPPolicyMissing       = infraerrors.BadRequest("PROXY_IP_POLICY_MISSING", "platform has no IP policy; configure one before allocating from proxy pools")
	ErrProxyIPPolicyNotFound      = infraerrors.NotFound("PROXY_IP_POLICY_NOT_FOUND", "platform IP policy not found")
	ErrProxyIPPolicyInvalid       = infraerrors.BadRequest("PROXY_IP_POLICY_INVALID", "invalid platform IP policy")
	ErrProxyIPPolicyPoolDenied    = infraerrors.BadRequest("PROXY_IP_POLICY_POOL_DENIED", "platform IP policy does not allow this proxy pool")
	ErrProxyIPPolicyPoolNotFound  = infraerrors.BadRequest("PROXY_IP_POLICY_POOL_NOT_FOUND", "platform IP policy references a missing proxy pool")
	ErrProxyIPPolicyGroupConflict = infraerrors.Conflict("PROXY_IP_POLICY_GROUP_CONFLICT", "account belongs to multiple groups with different IP policies")
	ErrProxyIPPolicyGroupNotFound = infraerrors.NotFound("PROXY_IP_POLICY_GROUP_NOT_FOUND", "group not found or has no IP policy")
)

type ProxyIPPolicy struct {
	ID int64
	// Platform 为平台策略的平台；分组策略取分组所属平台。
	Platform string
	// GroupID 非空表示分组策略（覆盖同平台的平台策略）。
	GroupID   *int64
	GroupName string
	Mode      string
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
	if p.GroupID != nil {
		// 分组策略的平台由仓储按分组填充。
		if *p.GroupID <= 0 {
			return ErrProxyIPPolicyInvalid
		}
	} else if p.Platform == "" {
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

func (s *ProxyPoolService) ListGroupPolicies(ctx context.Context) ([]ProxyIPPolicy, error) {
	return s.repo.ListGroupPolicies(ctx)
}

// loadPolicies 返回平台策略与分组策略（分组策略带 GroupID）。
func (s *ProxyPoolService) loadPolicies(ctx context.Context) ([]ProxyIPPolicy, error) {
	policies, err := s.repo.ListPolicies(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.repo.ListGroupPolicies(ctx)
	if err != nil {
		return nil, err
	}
	return append(policies, groups...), nil
}

func (s *ProxyPoolService) validatePolicy(ctx context.Context, p *ProxyIPPolicy) error {
	if err := normalizeProxyIPPolicy(p); err != nil {
		return err
	}
	for _, id := range p.PoolIDs {
		if _, err := s.repo.GetPool(ctx, id); err != nil {
			if errors.Is(err, ErrProxyPoolNotFound) {
				return ErrProxyIPPolicyPoolNotFound
			}
			return err
		}
	}
	return nil
}

// UpsertPolicy 新建或覆盖某平台的 IP 策略。
func (s *ProxyPoolService) UpsertPolicy(ctx context.Context, p *ProxyIPPolicy) (*ProxyIPPolicy, error) {
	p.GroupID = nil
	if err := s.validatePolicy(ctx, p); err != nil {
		return nil, err
	}
	if err := s.repo.UpsertPolicy(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// UpsertGroupPolicy 新建或覆盖某分组的 IP 策略，平台取分组所属平台。
func (s *ProxyPoolService) UpsertGroupPolicy(ctx context.Context, p *ProxyIPPolicy) (*ProxyIPPolicy, error) {
	if p.GroupID == nil {
		return nil, ErrProxyIPPolicyInvalid
	}
	if err := s.validatePolicy(ctx, p); err != nil {
		return nil, err
	}
	if err := s.repo.UpsertGroupPolicy(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteGroupPolicy 删除分组策略；之后该分组的账号回到平台策略（已有绑定不受影响）。
func (s *ProxyPoolService) DeleteGroupPolicy(ctx context.Context, groupID int64) error {
	return s.repo.DeleteGroupPolicy(ctx, groupID)
}

// DeletePolicy 删除平台策略；之后该平台不能再从代理池分配（已有绑定不受影响）。
func (s *ProxyPoolService) DeletePolicy(ctx context.Context, platform string) error {
	return s.repo.DeletePolicy(ctx, strings.TrimSpace(platform))
}

// policyFor 返回账号适用的策略：所在分组有同平台的分组策略时用分组策略，否则用平台策略。
// 多个分组策略内容不一致时拒绝（ErrProxyIPPolicyGroupConflict），需人工调整；都缺失时返回 ErrProxyIPPolicyMissing。
// 分组策略只对同平台账号生效：计数按账号平台统计，跨平台套用会算错口径。
func policyFor(policies []ProxyIPPolicy, platform string, groupIDs []int64) (*ProxyIPPolicy, error) {
	var group, base *ProxyIPPolicy
	for i := range policies {
		p := &policies[i]
		if p.Platform != platform {
			continue
		}
		if p.GroupID == nil {
			base = p
			continue
		}
		if !containsInt64(groupIDs, *p.GroupID) {
			continue
		}
		if group != nil && !samePolicyRules(group, p) {
			return nil, ErrProxyIPPolicyGroupConflict
		}
		if group == nil {
			group = p
		}
	}
	if group != nil {
		return group, nil
	}
	if base != nil {
		return base, nil
	}
	return nil, ErrProxyIPPolicyMissing
}

func equalIntPtr(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// samePolicyRules 报告两条策略的分配与故障规则是否一致（不比较归属）。
func samePolicyRules(a, b *ProxyIPPolicy) bool {
	if a.Mode != b.Mode || a.OnEgressFailure != b.OnEgressFailure || len(a.PoolIDs) != len(b.PoolIDs) ||
		!equalIntPtr(a.MaxLifetimeAccounts, b.MaxLifetimeAccounts) || !equalIntPtr(a.MaxActiveAccounts, b.MaxActiveAccounts) {
		return false
	}
	for i := range a.PoolIDs {
		if a.PoolIDs[i] != b.PoolIDs[i] {
			return false
		}
	}
	return true
}

// ProxyPoolRetiredPlatforms 返回该落地 IP 已退役（达到历史上限）的平台（只看平台策略）。
func ProxyPoolRetiredPlatforms(policies []ProxyIPPolicy, m *ProxyPoolMember) []string {
	out := make([]string, 0)
	for i := range policies {
		if policies[i].GroupID == nil && policies[i].retired(m) {
			out = append(out, policies[i].Platform)
		}
	}
	sort.Strings(out)
	return out
}

// ProxyPoolRetiredGroups 返回该落地 IP 已对其退役的分组策略名称。
func ProxyPoolRetiredGroups(policies []ProxyIPPolicy, m *ProxyPoolMember) []string {
	out := make([]string, 0)
	for i := range policies {
		if policies[i].GroupID != nil && policies[i].retired(m) {
			out = append(out, policies[i].GroupName)
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

// usedUpByChurn 报告该落地 IP 是否已被当前策略之外的某条 churn 策略用满（可转给长期账号继续用）。
// 同平台的 churn 分组策略用满的 IP 也算，例如 openai plus 用满后留给 openai pro。
func usedUpByChurn(policies []ProxyIPPolicy, current *ProxyIPPolicy, m *ProxyPoolMember) bool {
	for i := range policies {
		p := &policies[i]
		if samePolicyOwner(p, current) {
			continue
		}
		if p.Mode == ProxyIPPolicyModeChurn && p.retired(m) {
			return true
		}
	}
	return false
}

// samePolicyOwner 报告两条策略是否属于同一归属（同一平台策略或同一分组策略）。
func samePolicyOwner(a, b *ProxyIPPolicy) bool {
	if a.GroupID != nil || b.GroupID != nil {
		return a.GroupID != nil && b.GroupID != nil && *a.GroupID == *b.GroupID
	}
	return a.Platform == b.Platform
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
// sticky 先取已被其他 churn 策略用满的 IP，再取总在用账号最少的，把新 IP 留给消耗型平台。
func sortProxyPoolCandidates(policies []ProxyIPPolicy, policy *ProxyIPPolicy, candidates []ProxyPoolMember) {
	platform := policy.Platform
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := &candidates[i], &candidates[j]
		if policy.Mode == ProxyIPPolicyModeSticky {
			ua, ub := usedUpByChurn(policies, policy, a), usedUpByChurn(policies, policy, b)
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

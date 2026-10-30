package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 代理池（fork）：代理池分组、从池中为账号分配代理并锁定、账号-代理绑定记录与按平台退役。
//
// 约定：
//   - proxies.pool_id 为 NULL 视为默认代理池。
//   - proxy_account_bindings 由 accounts 上的触发器（迁移 248）在 proxy_id 变化 / 账号删除时维护，
//     因此所有改代理的路径（手动选择、过期回退、CRS 同步……）都会被记录。
//   - 从池中分配的绑定标记 locked=true；锁定期间管理端不允许修改账号代理，需先解绑（解除锁定）。
//   - 分配规则由 IP 策略决定（proxy_ip_policy.go）：分组策略优先于平台策略，都没有时不能从池分配；
//     计数按落地 IP 合并，“历史累计”含已解绑、已删除的账号，达到上限即对该平台永久退役。

const (
	ProxyBindingSourceManual = "manual"
	ProxyBindingSourcePool   = "pool"
)

var (
	ErrProxyPoolNotFound           = infraerrors.NotFound("PROXY_POOL_NOT_FOUND", "proxy pool not found")
	ErrProxyPoolNameExists         = infraerrors.Conflict("PROXY_POOL_NAME_EXISTS", "proxy pool name already exists")
	ErrProxyPoolDefaultDelete      = infraerrors.BadRequest("PROXY_POOL_DEFAULT_UNDELETABLE", "default proxy pool cannot be deleted")
	ErrProxyPoolNoAvailable        = infraerrors.Conflict("PROXY_POOL_NO_AVAILABLE_PROXY", "no available proxy in pool for this platform (all retired, inactive or expired)")
	ErrProxyPoolProxyNotInPool     = infraerrors.BadRequest("PROXY_POOL_PROXY_UNAVAILABLE", "proxy is not an available member of the proxy pool for this platform")
	ErrProxyPoolUnavailable        = infraerrors.ServiceUnavailable("PROXY_POOL_UNAVAILABLE", "proxy pool service is not available")
	ErrAccountProxyLocked          = infraerrors.Conflict("ACCOUNT_PROXY_LOCKED", "account proxy is locked by proxy pool binding; unbind it first")
	ErrProxyPoolShadowAccount      = infraerrors.BadRequest("PROXY_POOL_SHADOW_ACCOUNT", "shadow account inherits proxy from its parent account")
	ErrProxyPoolLimitInvalid       = infraerrors.BadRequest("PROXY_POOL_LIMIT_INVALID", "platform limit must be >= 1")
	ErrProxyPoolNameRequired       = infraerrors.BadRequest("PROXY_POOL_NAME_REQUIRED", "proxy pool name is required")
	ErrProxyPoolBindingNotFound    = infraerrors.NotFound("PROXY_POOL_BINDING_NOT_FOUND", "account has no active proxy binding")
	ErrProxyPoolReferencedByPolicy = infraerrors.Conflict("PROXY_POOL_REFERENCED_BY_POLICY", "proxy pool is referenced by a platform IP policy; remove it from the policy first")
	ErrProxyEgressHostExists       = infraerrors.Conflict("PROXY_EGRESS_HOST_EXISTS", "an egress proxy with the same landing IP already exists")
)

// ProxyPool 只是落地 IP 的库存分组；平台规则见 ProxyIPPolicy。
type ProxyPool struct {
	ID          int64
	Name        string
	Description string
	IsDefault   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time

	ProxyCount int64 // 仅列表填充
}

// ProxyPoolMember 是池内代理及其按平台的绑定计数。
type ProxyPoolMember struct {
	Proxy Proxy
	// HistoryCounts 平台 -> 历史累计绑定过的不同账号数（不含 ExcludeAccountID）。
	HistoryCounts map[string]int64
	// ActiveCounts 平台 -> 当前仍绑定的账号数。
	ActiveCounts map[string]int64
}

type ProxyAccountBinding struct {
	ID           int64
	ProxyID      int64
	ProxyName    string
	AccountID    int64
	AccountName  string
	Platform     string
	PoolID       *int64
	PoolName     string
	Source       string
	Locked       bool
	BoundAt      time.Time
	UnlockedAt   *time.Time
	UnboundAt    *time.Time
	UnbindReason string
}

type ProxyPoolRepository interface {
	ListPools(ctx context.Context) ([]ProxyPool, error)
	GetPool(ctx context.Context, id int64) (*ProxyPool, error)
	CreatePool(ctx context.Context, pool *ProxyPool) error
	UpdatePool(ctx context.Context, pool *ProxyPool) error
	DeletePool(ctx context.Context, id int64) error
	// AssignProxies 把代理移入代理池（默认池写 NULL）。
	AssignProxies(ctx context.Context, pool *ProxyPool, proxyIDs []int64) error
	// ListMembers 返回池内（未删除）代理及计数；excludeAccountID>0 时历史计数不含该账号。
	ListMembers(ctx context.Context, pool *ProxyPool, excludeAccountID int64) ([]ProxyPoolMember, error)
	ListBindingsByProxy(ctx context.Context, proxyID int64) ([]ProxyAccountBinding, error)
	GetOpenBinding(ctx context.Context, accountID int64) (*ProxyAccountBinding, error)
	// LockBinding 把账号当前未解除的绑定标记为来自代理池并锁定。
	LockBinding(ctx context.Context, accountID, poolID int64) error
	// UnlockBinding 解除账号当前绑定的锁定（代理保持不变）。
	UnlockBinding(ctx context.Context, accountID int64) error
	ListLockedAccountIDs(ctx context.Context, accountIDs []int64) ([]int64, error)
	// EgressHostTaken 报告是否已有其他未删除的 egress 代理使用同一落地 IP（host，忽略大小写）。
	EgressHostTaken(ctx context.Context, host string, excludeProxyID int64) (bool, error)

	ListPolicies(ctx context.Context) ([]ProxyIPPolicy, error)
	UpsertPolicy(ctx context.Context, policy *ProxyIPPolicy) error
	DeletePolicy(ctx context.Context, platform string) error
	// ListGroupPolicies 返回分组策略（迁移 260），Platform/GroupName 取自分组；已删除分组的策略不返回。
	ListGroupPolicies(ctx context.Context) ([]ProxyIPPolicy, error)
	// UpsertGroupPolicy 按 GroupID 新建或覆盖，并回填 Platform/GroupName；分组不存在时返回 ErrProxyIPPolicyGroupNotFound。
	UpsertGroupPolicy(ctx context.Context, policy *ProxyIPPolicy) error
	DeleteGroupPolicy(ctx context.Context, groupID int64) error
}

type ProxyPoolService struct {
	repo        ProxyPoolRepository
	accountRepo AccountRepository
	// latencyCache 提供探测到的国家，换 IP 时同国家优先（可为空）。
	latencyCache ProxyLatencyCache
	// mu 串行化“挑选代理 → 写入账号 → 锁定绑定”，避免并发创建超出平台上限（单实例内）。
	mu sync.Mutex
}

var activeProxyPoolService atomic.Pointer[ProxyPoolService]

func NewProxyPoolService(repo ProxyPoolRepository, accountRepo AccountRepository) *ProxyPoolService {
	return &ProxyPoolService{repo: repo, accountRepo: accountRepo}
}

// ProvideProxyPoolService 创建代理池服务并注册为进程内实例，供上游文件中的一行挂钩使用。
func ProvideProxyPoolService(repo ProxyPoolRepository, accountRepo AccountRepository) *ProxyPoolService {
	svc := NewProxyPoolService(repo, accountRepo)
	activeProxyPoolService.Store(svc)
	return svc
}

func normalizeProxyPoolInput(pool *ProxyPool) error {
	pool.Name = strings.TrimSpace(pool.Name)
	pool.Description = strings.TrimSpace(pool.Description)
	if pool.Name == "" {
		return ErrProxyPoolNameRequired
	}
	return nil
}

func (s *ProxyPoolService) ListPools(ctx context.Context) ([]ProxyPool, error) {
	return s.repo.ListPools(ctx)
}

func (s *ProxyPoolService) CreatePool(ctx context.Context, pool *ProxyPool) (*ProxyPool, error) {
	if err := normalizeProxyPoolInput(pool); err != nil {
		return nil, err
	}
	pool.IsDefault = false
	if err := s.repo.CreatePool(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *ProxyPoolService) UpdatePool(ctx context.Context, id int64, name, description *string) (*ProxyPool, error) {
	pool, err := s.repo.GetPool(ctx, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		pool.Name = *name
	}
	if description != nil {
		pool.Description = *description
	}
	if err := normalizeProxyPoolInput(pool); err != nil {
		return nil, err
	}
	if err := s.repo.UpdatePool(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *ProxyPoolService) DeletePool(ctx context.Context, id int64) error {
	pool, err := s.repo.GetPool(ctx, id)
	if err != nil {
		return err
	}
	if pool.IsDefault {
		return ErrProxyPoolDefaultDelete
	}
	// 被策略引用的池不能删：直接从 pool_ids 里去掉可能让列表变空，变成“全部池”而放宽限制。
	policies, err := s.loadPolicies(ctx)
	if err != nil {
		return err
	}
	for i := range policies {
		if len(policies[i].PoolIDs) > 0 && policies[i].AllowsPool(id) {
			return ErrProxyPoolReferencedByPolicy
		}
	}
	return s.repo.DeletePool(ctx, id)
}

func (s *ProxyPoolService) AssignProxies(ctx context.Context, poolID int64, proxyIDs []int64) error {
	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return err
	}
	return s.repo.AssignProxies(ctx, pool, proxyIDs)
}

// ListMembers 返回池内代理、计数与当前平台、分组策略，供管理页展示退役状态。
func (s *ProxyPoolService) ListMembers(ctx context.Context, poolID int64) (*ProxyPool, []ProxyPoolMember, []ProxyIPPolicy, error) {
	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return nil, nil, nil, err
	}
	members, err := s.repo.ListMembers(ctx, pool, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	policies, err := s.loadPolicies(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return pool, members, policies, nil
}

func (s *ProxyPoolService) ListBindingsByProxy(ctx context.Context, proxyID int64) ([]ProxyAccountBinding, error) {
	return s.repo.ListBindingsByProxy(ctx, proxyID)
}

func (s *ProxyPoolService) GetAccountBinding(ctx context.Context, accountID int64) (*ProxyAccountBinding, error) {
	return s.repo.GetOpenBinding(ctx, accountID)
}

// PreviewProxy 返回下一次分配会选中的代理（不占用），供 OAuth 流程先用该代理完成授权。
// groupIDs 为将要绑定的分组，用于匹配分组策略。
func (s *ProxyPoolService) PreviewProxy(ctx context.Context, poolID int64, platform string, groupIDs []int64) (*Proxy, error) {
	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return nil, err
	}
	return s.pickProxy(ctx, pool, platform, groupIDs, 0, nil, false)
}

// pickProxy 按账号适用的策略（分组策略优先于平台策略）在池内可用落地 IP 中挑选（排序见 sortProxyPoolCandidates）。
// 没有适用策略或策略不允许该池时拒绝。
// preferredID 非空时：strict=true 要求必须是该代理，否则在其可用时优先选它。
func (s *ProxyPoolService) pickProxy(ctx context.Context, pool *ProxyPool, platform string, groupIDs []int64, excludeAccountID int64, preferredID *int64, strict bool) (*Proxy, error) {
	policies, err := s.loadPolicies(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := policyFor(policies, platform, groupIDs)
	if err != nil {
		return nil, err
	}
	if !policy.AllowsPool(pool.ID) {
		return nil, ErrProxyIPPolicyPoolDenied
	}
	members, err := s.repo.ListMembers(ctx, pool, excludeAccountID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	candidates := make([]ProxyPoolMember, 0, len(members))
	for i := range members {
		if proxyPoolMemberAvailable(policy, &members[i], now) {
			candidates = append(candidates, members[i])
		}
	}
	if preferredID != nil {
		for i := range candidates {
			if candidates[i].Proxy.ID == *preferredID {
				p := candidates[i].Proxy
				return &p, nil
			}
		}
		if strict {
			return nil, ErrProxyPoolProxyNotInPool
		}
	}
	if len(candidates) == 0 {
		return nil, ErrProxyPoolNoAvailable
	}
	sortProxyPoolCandidates(policies, policy, candidates)
	p := candidates[0].Proxy
	return &p, nil
}

// AssignAccount 为已有账号从代理池分配代理并锁定：当前代理在池内且可用时保留，否则换成池内用量最少的代理。
func (s *ProxyPoolService) AssignAccount(ctx context.Context, accountID, poolID int64) (*Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return nil, err
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.IsCredentialShadow() {
		return nil, ErrProxyPoolShadowAccount
	}
	binding, err := s.repo.GetOpenBinding(ctx, accountID)
	if err != nil && !errors.Is(err, ErrProxyPoolBindingNotFound) {
		return nil, err
	}
	if binding != nil && binding.Locked {
		return nil, ErrAccountProxyLocked
	}
	proxy, err := s.pickProxy(ctx, pool, account.Platform, account.GroupIDs, accountID, account.ProxyID, false)
	if err != nil {
		return nil, err
	}
	if account.ProxyID == nil || *account.ProxyID != proxy.ID {
		id := proxy.ID
		account.ProxyID = &id
		account.Proxy = nil
		if account.Extra != nil {
			// 出口变了，上游计费探测快照作废（与 UpdateAccount 改代理时一致）。
			delete(account.Extra, UpstreamBillingProbeExtraKey)
		}
		if err := s.accountRepo.Update(ctx, account); err != nil {
			return nil, err
		}
		if err := propagateAccountProxyToShadows(ctx, s.accountRepo, accountID, account.ProxyID); err != nil {
			return nil, err
		}
	}
	if err := s.repo.LockBinding(ctx, accountID, pool.ID); err != nil {
		return nil, err
	}
	return proxy, nil
}

// UnbindAccount 解除账号代理绑定的锁定；代理保持不变，之后可在账号编辑中自由更换。
func (s *ProxyPoolService) UnbindAccount(ctx context.Context, accountID int64) error {
	return s.repo.UnlockBinding(ctx, accountID)
}

// ---------------------------------------------------------------------------
// 上游文件中的挂钩（admin_account.go / admin_proxy.go），服务未注入时均为空操作。
// ---------------------------------------------------------------------------

// beginProxyPoolAccountCreate 在 CreateAccount 开头调用：指定了代理池时挑选代理写入 input.ProxyID，
// 返回的 release 必须在账号创建（及 finishProxyPoolAccountCreate）之后调用。
// 同时指定 ProxyID 时（OAuth / 批量 RT 先用预览代理授权），该代理仍可用则沿用，否则回退到池内用量最少的代理。
// groupIDs 为账号最终绑定的分组（含自动绑定的默认分组），用于匹配分组策略。
func beginProxyPoolAccountCreate(ctx context.Context, input *CreateAccountInput, groupIDs []int64) (func(), error) {
	if input == nil || input.ProxyPoolID == nil {
		return func() {}, nil
	}
	s := activeProxyPoolService.Load()
	if s == nil {
		return nil, ErrProxyPoolUnavailable
	}
	pool, err := s.repo.GetPool(ctx, *input.ProxyPoolID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	var preferred *int64
	if input.ProxyID != nil && *input.ProxyID > 0 {
		preferred = input.ProxyID
	}
	proxy, err := s.pickProxy(ctx, pool, input.Platform, groupIDs, 0, preferred, false)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	id := proxy.ID
	input.ProxyID = &id
	return s.mu.Unlock, nil
}

// finishProxyPoolAccountCreate 在账号创建成功后锁定其代理池绑定。
func finishProxyPoolAccountCreate(ctx context.Context, input *CreateAccountInput, account *Account) error {
	if input == nil || input.ProxyPoolID == nil || account == nil {
		return nil
	}
	s := activeProxyPoolService.Load()
	if s == nil {
		return ErrProxyPoolUnavailable
	}
	if err := s.repo.LockBinding(ctx, account.ID, *input.ProxyPoolID); err != nil {
		slog.Error("proxy_pool_lock_binding_failed", "account_id", account.ID, "pool_id", *input.ProxyPoolID, "error", err)
		return err
	}
	return nil
}

// checkAccountProxyPoolLock 在管理端修改账号代理前调用：代理确实变化且绑定被锁定时拒绝。
// requested 为请求中的 proxy_id（nil=未提供，0=清除）。
func checkAccountProxyPoolLock(ctx context.Context, account *Account, requested *int64) error {
	if account == nil || requested == nil || !proxyIDChanged(account.ProxyID, requested) {
		return nil
	}
	s := activeProxyPoolService.Load()
	if s == nil {
		return nil
	}
	locked, err := s.repo.ListLockedAccountIDs(ctx, []int64{account.ID})
	if err != nil {
		return err
	}
	if len(locked) > 0 {
		return ErrAccountProxyLocked
	}
	return nil
}

// checkAccountsProxyPoolLock 是批量编辑版本：任一目标账号的代理会变化且已锁定即整体拒绝。
func checkAccountsProxyPoolLock(ctx context.Context, accounts []*Account, requested *int64) error {
	if requested == nil {
		return nil
	}
	s := activeProxyPoolService.Load()
	if s == nil {
		return nil
	}
	ids := make([]int64, 0, len(accounts))
	for _, acc := range accounts {
		if acc != nil && proxyIDChanged(acc.ProxyID, requested) {
			ids = append(ids, acc.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	locked, err := s.repo.ListLockedAccountIDs(ctx, ids)
	if err != nil {
		return err
	}
	if len(locked) > 0 {
		return infraerrors.Newf(http.StatusConflict, "ACCOUNT_PROXY_LOCKED",
			"accounts %v have proxy locked by proxy pool binding; unbind them first", locked)
	}
	return nil
}

func proxyIDChanged(current, requested *int64) bool {
	var next int64
	if requested != nil {
		next = *requested
	}
	var cur int64
	if current != nil {
		cur = *current
	}
	return cur != next
}

// checkProxyEgressHost 校验落地 IP（host）不与其他代理重复（仅在新建或改 host 时校验，存量重复不阻断编辑）。
// prev 为编辑前的代理（新建时为 nil）。
func checkProxyEgressHost(ctx context.Context, p *Proxy, prev *Proxy) error {
	if prev != nil && strings.EqualFold(prev.Host, p.Host) {
		return nil
	}
	s := activeProxyPoolService.Load()
	if s == nil {
		return nil
	}
	taken, err := s.repo.EgressHostTaken(ctx, strings.TrimSpace(p.Host), p.ID)
	if err != nil {
		return err
	}
	if taken {
		return ErrProxyEgressHostExists
	}
	return nil
}

// normalizeProxyPoolRef 校验代理创建/编辑时指定的代理池存在；默认池统一存为 NULL。
func normalizeProxyPoolRef(ctx context.Context, poolID *int64) (*int64, error) {
	if poolID == nil || *poolID <= 0 {
		return nil, nil
	}
	s := activeProxyPoolService.Load()
	if s == nil {
		return nil, ErrProxyPoolUnavailable
	}
	pool, err := s.repo.GetPool(ctx, *poolID)
	if err != nil {
		return nil, err
	}
	if pool.IsDefault {
		return nil, nil
	}
	id := pool.ID
	return &id, nil
}

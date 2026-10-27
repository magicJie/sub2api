package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 代理池（fork）：落地 IP 健康巡检与一键处置，见 docs/PROXY_IP_MODEL.md 第 5 节。
//
// 巡检只看有账号在用的落地代理（role=egress）。故障分两层：
//   - transport：前置链路不可用（首跳组 / 前置代理挂了、链路无法解析）。只告警，不动账号——
//     请求本身已 fail-closed，换落地 IP 也没用，而且一个机场挂了不应暂停所有长期账号。
//   - egress：传输层健康，但经该落地代理不通。按平台策略处置账号：
//     sticky 或 on_egress_failure=pause → 暂停调度并告警；churn + reassign → 自动换池内其他 IP。
//
// 巡检间隔与失败阈值可在管理端配置（ProxyEgressHealthConfig）；连续失败达到阈值才处置，避免网络抖动误伤；探测恢复后故障自动关闭，但暂停的账号不自动恢复，
// 需要人工「恢复」或「换 IP」（长期账号换 IP 是风控信号，必须人来定）。

const (
	ProxyIncidentLayerEgress    = "egress"
	ProxyIncidentLayerTransport = "transport"

	ProxyIncidentStatusOpen     = "open"
	ProxyIncidentStatusResolved = "resolved"

	ProxyIncidentActionPaused     = "paused"
	ProxyIncidentActionReassigned = "reassigned"
	ProxyIncidentActionAlertOnly  = "alert_only"

	// 巡检配置默认值与取值范围（管理端可改，存于 settings，下一轮生效）。
	ProxyEgressDefaultIntervalSeconds = 120
	ProxyEgressDefaultFailThreshold   = 3
	proxyEgressMinIntervalSeconds     = 30
	proxyEgressMaxIntervalSeconds     = 3600
	proxyEgressMaxFailThreshold       = 20
	// SettingKeyProxyEgressHealth 是巡检配置（JSON）的 settings 键。
	SettingKeyProxyEgressHealth = "proxy_egress_health_config"

	proxyEgressTick          = 15 * time.Second // 调度粒度：每 tick 读一次配置，到点才巡检
	proxyEgressProbeTimeout  = 20 * time.Second
	proxyEgressLeaderLockKey = "proxy:egress:health:leader"
	proxyEgressLeaderLockTTL = 10 * time.Minute
)

// ProxyEgressHealthConfig 是巡检配置：Enabled 关闭后不巡检、不处置（已有故障保持原状）。
type ProxyEgressHealthConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalSeconds int  `json:"interval_seconds"`
	FailThreshold   int  `json:"fail_threshold"`
}

func DefaultProxyEgressHealthConfig() ProxyEgressHealthConfig {
	return ProxyEgressHealthConfig{Enabled: true, IntervalSeconds: ProxyEgressDefaultIntervalSeconds, FailThreshold: ProxyEgressDefaultFailThreshold}
}

var ErrProxyEgressConfigInvalid = infraerrors.BadRequest("PROXY_EGRESS_CONFIG_INVALID",
	"interval_seconds must be 30-3600 and fail_threshold must be 1-20")

func validateProxyEgressHealthConfig(c ProxyEgressHealthConfig) error {
	if c.IntervalSeconds < proxyEgressMinIntervalSeconds || c.IntervalSeconds > proxyEgressMaxIntervalSeconds ||
		c.FailThreshold < 1 || c.FailThreshold > proxyEgressMaxFailThreshold {
		return ErrProxyEgressConfigInvalid
	}
	return nil
}

// loadProxyEgressHealthConfig 读取巡检配置；未设置或损坏时用默认值（损坏的值不会让巡检停掉）。
func loadProxyEgressHealthConfig(ctx context.Context, repo SettingRepository) ProxyEgressHealthConfig {
	cfg := DefaultProxyEgressHealthConfig()
	if repo == nil {
		return cfg
	}
	raw, err := repo.GetValue(ctx, SettingKeyProxyEgressHealth)
	if err != nil || strings.TrimSpace(raw) == "" {
		return cfg
	}
	var stored ProxyEgressHealthConfig
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || validateProxyEgressHealthConfig(stored) != nil {
		slog.Warn("proxy_egress_health_config_invalid", "value", raw)
		return cfg
	}
	return stored
}

var (
	ErrProxyIncidentNotPaused = infraerrors.BadRequest("PROXY_INCIDENT_ACCOUNT_NOT_PAUSED", "account is not paused by a landing IP failure")
	ErrProxyHealthUnavailable = infraerrors.ServiceUnavailable("PROXY_HEALTH_UNAVAILABLE", "proxy health service is not available")
)

type ProxyEgressIncident struct {
	ID            int64
	ProxyID       int64
	ProxyName     string
	Layer         string
	Status        string
	LastError     string
	FailCount     int
	FirstFailedAt time.Time
	LastCheckedAt time.Time
	ResolvedAt    *time.Time
	HandledAt     *time.Time
	AlertEventID  *int64
	Accounts      []ProxyEgressIncidentAccount
}

type ProxyEgressIncidentAccount struct {
	ID         int64
	IncidentID int64
	AccountID  int64
	Action     string
	ToProxyID  *int64
	Message    string
	CreatedAt  time.Time
	ResumedAt  *time.Time

	// 仅列表填充
	AccountName    string
	Platform       string
	Schedulable    bool
	CurrentProxyID *int64
}

// ProxyEgressIncidentRepository 是故障记录的持久化（由 proxyPoolRepository 实现）。
type ProxyEgressIncidentRepository interface {
	ListProxyAccountIDs(ctx context.Context, proxyID int64) ([]int64, error)
	ListOpenIncidentProxyIDs(ctx context.Context) ([]int64, error)
	GetOpenIncident(ctx context.Context, proxyID int64) (*ProxyEgressIncident, error)
	RecordIncidentFailure(ctx context.Context, proxyID int64, layer, lastError string) (*ProxyEgressIncident, error)
	MarkIncidentHandled(ctx context.Context, incidentID int64, alertEventID *int64) error
	ResolveIncident(ctx context.Context, incidentID int64) error
	RecordIncidentAccount(ctx context.Context, rec *ProxyEgressIncidentAccount) error
	GetPausedIncidentAccount(ctx context.Context, accountID int64) (*ProxyEgressIncidentAccount, error)
	MarkAccountResumed(ctx context.Context, accountID int64) error
	ListIncidents(ctx context.Context, resolvedLimit int) ([]ProxyEgressIncident, error)
}

// proxyEgressAlertSink 把故障写入运维告警（ops_alert_events，走现有告警页面/邮件配置）。
type proxyEgressAlertSink interface {
	IsMonitoringEnabled(ctx context.Context) bool
	CreateAlertEvent(ctx context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error)
	UpdateAlertEventStatus(ctx context.Context, eventID int64, status string, resolvedAt *time.Time) error
}

type ProxyEgressHealthService struct {
	pool      *ProxyPoolService
	incidents ProxyEgressIncidentRepository
	proxyRepo ProxyRepository
	prober    ProxyExitInfoProber
	alerts    proxyEgressAlertSink
	lockCache LeaderLockCache
	settings  SettingRepository
	owner     string
	tick      time.Duration
	now       func() time.Time
	lastRun   time.Time

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewProxyEgressHealthService(pool *ProxyPoolService, incidents ProxyEgressIncidentRepository, proxyRepo ProxyRepository,
	prober ProxyExitInfoProber, alerts proxyEgressAlertSink, lockCache LeaderLockCache, settings SettingRepository, tick time.Duration) *ProxyEgressHealthService {
	return &ProxyEgressHealthService{
		pool: pool, incidents: incidents, proxyRepo: proxyRepo, prober: prober, alerts: alerts, lockCache: lockCache,
		settings: settings, owner: fmt.Sprintf("proxy-egress-%d", time.Now().UnixNano()), tick: tick, now: time.Now,
		stopCh: make(chan struct{}),
	}
}

var activeProxyEgressHealthService sync.Map // 单例："svc" -> *ProxyEgressHealthService

// ProvideProxyEgressHealthService 创建并启动落地 IP 巡检。
func ProvideProxyEgressHealthService(pool *ProxyPoolService, poolRepo ProxyPoolRepository, proxyRepo ProxyRepository,
	prober ProxyExitInfoProber, opsService *OpsService, lockCache LeaderLockCache, latencyCache ProxyLatencyCache,
	settingRepo SettingRepository) *ProxyEgressHealthService {
	pool.latencyCache = latencyCache
	incidents, _ := poolRepo.(ProxyEgressIncidentRepository)
	var alerts proxyEgressAlertSink
	if opsService != nil {
		alerts = opsService
	}
	svc := NewProxyEgressHealthService(pool, incidents, proxyRepo, prober, alerts, lockCache, settingRepo, proxyEgressTick)
	activeProxyEgressHealthService.Store("svc", svc)
	svc.Start()
	return svc
}

func (s *ProxyEgressHealthService) Start() {
	if s == nil || s.incidents == nil || s.prober == nil || s.tick <= 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *ProxyEgressHealthService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *ProxyEgressHealthService) config(ctx context.Context) ProxyEgressHealthConfig {
	return loadProxyEgressHealthConfig(ctx, s.settings)
}

// runOnce 每个 tick 调用一次：读配置，关闭或未到间隔则跳过。
func (s *ProxyEgressHealthService) runOnce() {
	cfg := s.config(context.Background())
	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	now := s.now()
	if !cfg.Enabled || now.Sub(s.lastRun) < interval {
		return
	}
	s.lastRun = now
	ctx, cancel := context.WithTimeout(context.Background(), interval)
	defer cancel()
	if s.lockCache != nil {
		ok, err := s.lockCache.TryAcquireLeaderLock(ctx, proxyEgressLeaderLockKey, s.owner, proxyEgressLeaderLockTTL)
		if err != nil || !ok {
			return
		}
		defer func() { _ = s.lockCache.ReleaseLeaderLock(context.Background(), proxyEgressLeaderLockKey, s.owner) }()
	}
	if err := s.CheckAll(ctx); err != nil {
		slog.Warn("proxy_egress_health_check_failed", "error", err)
	}
}

// CheckAll 巡检所有有账号在用的落地代理，以及仍有未恢复故障的代理（以便自动关闭故障）。
func (s *ProxyEgressHealthService) CheckAll(ctx context.Context) error {
	proxies, err := s.proxyRepo.ListActiveWithAccountCount(ctx)
	if err != nil {
		return err
	}
	open, err := s.incidents.ListOpenIncidentProxyIDs(ctx)
	if err != nil {
		return err
	}
	openSet := make(map[int64]bool, len(open))
	for _, id := range open {
		openSet[id] = true
	}
	for i := range proxies {
		p := &proxies[i].Proxy
		if NormalizeProxyRole(p.Role) != ProxyRoleEgress || (proxies[i].AccountCount == 0 && !openSet[p.ID]) {
			continue
		}
		delete(openSet, p.ID)
		if err := s.CheckProxy(ctx, p); err != nil {
			slog.Warn("proxy_egress_check_proxy_failed", "proxy_id", p.ID, "error", err)
		}
	}
	// 剩下的是已停用或已删除的代理：不再探测。已删除的关闭故障（无从处理）；停用的保持打开等人工处理。
	for id := range openSet {
		if _, err := s.proxyRepo.GetByID(ctx, id); errors.Is(err, ErrProxyNotFound) {
			if err := s.recover(ctx, id); err != nil {
				slog.Warn("proxy_egress_resolve_deleted_failed", "proxy_id", id, "error", err)
			}
		}
	}
	return nil
}

// CheckProxy 探测一个落地代理并更新故障与账号处置。
func (s *ProxyEgressHealthService) CheckProxy(ctx context.Context, p *Proxy) error {
	probeCtx, cancel := context.WithTimeout(ctx, proxyEgressProbeTimeout)
	defer cancel()
	layer, probeErr := s.probe(probeCtx, p)
	if probeErr == nil {
		return s.recover(ctx, p.ID)
	}
	incident, err := s.incidents.RecordIncidentFailure(ctx, p.ID, layer, truncateIncidentError(probeErr.Error()))
	if err != nil {
		return err
	}
	if incident.HandledAt != nil || incident.FailCount < s.config(ctx).FailThreshold {
		return nil
	}
	return s.handleIncident(ctx, p, incident)
}

// probe 返回故障层级：链路无法解析或首跳组没有健康节点算 transport，其余探测失败算 egress。
func (s *ProxyEgressHealthService) probe(ctx context.Context, p *Proxy) (string, error) {
	if _, chainErr := DescribeProxyChain(p); chainErr != "" {
		return ProxyIncidentLayerTransport, errors.New(chainErr)
	}
	if groupID := effectiveHopGroupID(p); groupID != nil {
		if healthy, err := hopGroupHealthy(*groupID); !healthy {
			if err == nil {
				err = errors.New("first-hop group has no healthy member")
			}
			return ProxyIncidentLayerTransport, err
		}
	}
	if _, _, err := s.prober.ProbeProxy(ctx, p.URL()); err != nil {
		return ProxyIncidentLayerEgress, err
	}
	return "", nil
}

// effectiveHopGroupID 返回落地代理直接使用的首跳组（hop_group 模式或跟随默认首跳组）。
func effectiveHopGroupID(p *Proxy) *int64 {
	switch NormalizeUpstreamMode(p.UpstreamMode) {
	case UpstreamModeHopGroup:
		return p.UpstreamHopGroupID
	case UpstreamModeDefault:
		return DefaultUpstreamHopGroupID()
	}
	return nil
}

var hopGroupHealthy = func(groupID int64) (bool, error) {
	svc := activeProxyChainService.Load()
	if svc == nil {
		return true, nil // 没有首跳组运行时（测试/未启用）：不判定为传输层故障
	}
	status, err := svc.HopGroupStatus(groupID)
	if err != nil {
		return false, err
	}
	return status.Selected != "", nil
}

func truncateIncidentError(msg string) string {
	const max = 500
	if len(msg) > max {
		return msg[:max]
	}
	return msg
}

func (s *ProxyEgressHealthService) recover(ctx context.Context, proxyID int64) error {
	incident, err := s.incidents.GetOpenIncident(ctx, proxyID)
	if err != nil || incident == nil {
		return err
	}
	if err := s.incidents.ResolveIncident(ctx, incident.ID); err != nil {
		return err
	}
	if incident.AlertEventID != nil && s.alerts != nil {
		now := s.now()
		_ = s.alerts.UpdateAlertEventStatus(ctx, *incident.AlertEventID, OpsAlertStatusResolved, &now)
	}
	return nil
}

func (s *ProxyEgressHealthService) handleIncident(ctx context.Context, p *Proxy, incident *ProxyEgressIncident) error {
	var paused, reassigned, alertOnly int
	if incident.Layer == ProxyIncidentLayerEgress {
		ids, err := s.incidents.ListProxyAccountIDs(ctx, p.ID)
		if err != nil {
			return err
		}
		policies, err := s.pool.repo.ListPolicies(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			switch s.handleAccount(ctx, incident, id, policies) {
			case ProxyIncidentActionPaused:
				paused++
			case ProxyIncidentActionReassigned:
				reassigned++
			default:
				alertOnly++
			}
		}
	}
	if incident.AlertEventID != nil && s.alerts != nil {
		// 故障层级变化后重新处置：旧告警作废，换成新告警。
		now := s.now()
		_ = s.alerts.UpdateAlertEventStatus(ctx, *incident.AlertEventID, OpsAlertStatusResolved, &now)
	}
	alertID := s.raiseAlert(ctx, p, incident, paused, reassigned, alertOnly)
	return s.incidents.MarkIncidentHandled(ctx, incident.ID, alertID)
}

// handleAccount 按平台策略处置单个账号，返回实际动作。
func (s *ProxyEgressHealthService) handleAccount(ctx context.Context, incident *ProxyEgressIncident, accountID int64, policies []ProxyIPPolicy) string {
	rec := &ProxyEgressIncidentAccount{IncidentID: incident.ID, AccountID: accountID, Action: ProxyIncidentActionAlertOnly}
	defer func() {
		if err := s.incidents.RecordIncidentAccount(ctx, rec); err != nil {
			slog.Warn("proxy_egress_record_account_failed", "account_id", accountID, "error", err)
		}
	}()
	account, err := s.pool.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		rec.Message = err.Error()
		return rec.Action
	}
	policy, _ := policyFor(policies, account.Platform)
	if policy != nil && policy.Mode == ProxyIPPolicyModeChurn && policy.OnEgressFailure == ProxyIPFailureReassign {
		proxy, err := s.pool.ReplaceAccountProxy(ctx, account.ID, nil, incident.ProxyID)
		if err == nil {
			rec.Action, rec.ToProxyID = ProxyIncidentActionReassigned, &proxy.ID
			return rec.Action
		}
		rec.Message = "auto reassign failed: " + err.Error()
	}
	if !account.Schedulable {
		// 原本就不可调度：不改状态，避免人工恢复时误把手动关掉的账号打开。
		rec.Message = strings.TrimSpace(rec.Message + " account already unschedulable")
		return rec.Action
	}
	if err := s.pool.accountRepo.SetSchedulable(ctx, accountID, false); err != nil {
		rec.Message = strings.TrimSpace(rec.Message + " pause failed: " + err.Error())
		return rec.Action
	}
	rec.Action = ProxyIncidentActionPaused
	return rec.Action
}

func (s *ProxyEgressHealthService) raiseAlert(ctx context.Context, p *Proxy, incident *ProxyEgressIncident, paused, reassigned, alertOnly int) *int64 {
	slog.Warn("proxy_egress_incident", "proxy_id", p.ID, "proxy_name", p.Name, "layer", incident.Layer,
		"paused", paused, "reassigned", reassigned, "alert_only", alertOnly, "error", incident.LastError)
	if s.alerts == nil || !s.alerts.IsMonitoringEnabled(ctx) {
		return nil
	}
	severity, title := "P1", fmt.Sprintf("落地 IP 故障：%s（%s）", p.Name, p.Host)
	if incident.Layer == ProxyIncidentLayerTransport {
		severity, title = "P2", fmt.Sprintf("前置链路故障：%s（%s）", p.Name, p.Host)
	}
	desc := fmt.Sprintf("连续 %d 次探测失败：%s。暂停 %d 个账号，自动换 IP %d 个，仅告警 %d 个。请在代理池页面的故障列表处理。",
		incident.FailCount, incident.LastError, paused, reassigned, alertOnly)
	event, err := s.alerts.CreateAlertEvent(ctx, &OpsAlertEvent{
		Severity: severity, Status: OpsAlertStatusFiring, Title: title, Description: desc,
		Dimensions: map[string]any{"source": "proxy_egress", "proxy_id": p.ID, "layer": incident.Layer, "incident_id": incident.ID},
		FiredAt:    s.now(),
	})
	if err != nil {
		slog.Warn("proxy_egress_alert_failed", "proxy_id", p.ID, "error", err)
		return nil
	}
	return &event.ID
}

// ---------------------------------------------------------------------------
// 一键操作（管理端）
// ---------------------------------------------------------------------------

func (s *ProxyPoolService) incidentRepo() (ProxyEgressIncidentRepository, error) {
	repo, ok := s.repo.(ProxyEgressIncidentRepository)
	if !ok {
		return nil, ErrProxyHealthUnavailable
	}
	return repo, nil
}

func (s *ProxyPoolService) ListIncidents(ctx context.Context) ([]ProxyEgressIncident, error) {
	repo, err := s.incidentRepo()
	if err != nil {
		return nil, err
	}
	return repo.ListIncidents(ctx, 50)
}

// ProxyReplaceCandidate 是换 IP 时的候选落地 IP。
type ProxyReplaceCandidate struct {
	Proxy         Proxy
	PoolID        int64
	PoolName      string
	HistoryCounts map[string]int64
	ActiveCounts  map[string]int64
	Country       string
	SameCountry   bool
}

// ReplaceCandidates 列出账号可换的落地 IP：策略允许的池内、对该平台可用、排除当前代理和有未恢复故障的代理，
// 与当前 IP 同国家（来自探测缓存）的优先，其余按策略排序。
func (s *ProxyPoolService) ReplaceCandidates(ctx context.Context, accountID int64) ([]ProxyReplaceCandidate, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.IsCredentialShadow() {
		return nil, ErrProxyPoolShadowAccount
	}
	var exclude int64
	if account.ProxyID != nil {
		exclude = *account.ProxyID
	}
	cands, err := s.replaceCandidates(ctx, account, exclude)
	if err != nil {
		return nil, err
	}
	if s.latencyCache != nil && exclude > 0 && len(cands) > 0 {
		ids := []int64{exclude}
		for i := range cands {
			ids = append(ids, cands[i].Proxy.ID)
		}
		if infos, err := s.latencyCache.GetProxyLatencies(ctx, ids); err == nil {
			country := func(id int64) string {
				if info := infos[id]; info != nil {
					return info.CountryCode
				}
				return ""
			}
			if cur := country(exclude); cur != "" {
				for i := range cands {
					cands[i].Country = country(cands[i].Proxy.ID)
					cands[i].SameCountry = cands[i].Country == cur
				}
				sort.SliceStable(cands, func(i, j int) bool { return cands[i].SameCountry && !cands[j].SameCountry })
			}
		}
	}
	return cands, nil
}

func (s *ProxyPoolService) replaceCandidates(ctx context.Context, account *Account, excludeProxyID int64) ([]ProxyReplaceCandidate, error) {
	policies, err := s.repo.ListPolicies(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := policyFor(policies, account.Platform)
	if err != nil {
		return nil, err
	}
	pools, err := s.repo.ListPools(ctx)
	if err != nil {
		return nil, err
	}
	var broken map[int64]bool
	if repo, err := s.incidentRepo(); err == nil {
		ids, err := repo.ListOpenIncidentProxyIDs(ctx)
		if err != nil {
			return nil, err
		}
		broken = make(map[int64]bool, len(ids))
		for _, id := range ids {
			broken[id] = true
		}
	}
	now := time.Now()
	out := make([]ProxyReplaceCandidate, 0)
	members := make([]ProxyPoolMember, 0)
	poolOf := map[int64]*ProxyPool{}
	for i := range pools {
		pool := &pools[i]
		if !policy.AllowsPool(pool.ID) {
			continue
		}
		ms, err := s.repo.ListMembers(ctx, pool, account.ID)
		if err != nil {
			return nil, err
		}
		for j := range ms {
			m := ms[j]
			if m.Proxy.ID == excludeProxyID || broken[m.Proxy.ID] || !proxyPoolMemberAvailable(policy, &m, now) {
				continue
			}
			poolOf[m.Proxy.ID] = pool
			members = append(members, m)
		}
	}
	sortProxyPoolCandidates(policies, policy, members)
	for _, m := range members {
		pool := poolOf[m.Proxy.ID]
		out = append(out, ProxyReplaceCandidate{Proxy: m.Proxy, PoolID: pool.ID, PoolName: pool.Name,
			HistoryCounts: m.HistoryCounts, ActiveCounts: m.ActiveCounts})
	}
	return out, nil
}

// ReplaceAccountProxy 给账号换落地 IP：targetProxyID 为空时自动选第一个候选（排除 excludeProxyID 与当前代理）。
// 换绑后锁定到新 IP 所在的池，解除故障暂停并恢复调度（若是因故障被暂停的）。
func (s *ProxyPoolService) ReplaceAccountProxy(ctx context.Context, accountID int64, targetProxyID *int64, excludeProxyID int64) (*Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.IsCredentialShadow() {
		return nil, ErrProxyPoolShadowAccount
	}
	if excludeProxyID == 0 && account.ProxyID != nil {
		excludeProxyID = *account.ProxyID
	}
	cands, err := s.replaceCandidates(ctx, account, excludeProxyID)
	if err != nil {
		return nil, err
	}
	var chosen *ProxyReplaceCandidate
	for i := range cands {
		if targetProxyID == nil || cands[i].Proxy.ID == *targetProxyID {
			chosen = &cands[i]
			break
		}
	}
	if chosen == nil {
		if targetProxyID != nil {
			return nil, ErrProxyPoolProxyNotInPool
		}
		return nil, ErrProxyPoolNoAvailable
	}
	// 系统换绑：先解锁当前绑定，否则触发器会把新绑定沿用原池锁定（来源池不对）。
	if err := s.repo.UnlockBinding(ctx, accountID); err != nil && !errors.Is(err, ErrProxyPoolBindingNotFound) {
		return nil, err
	}
	id := chosen.Proxy.ID
	account.ProxyID = &id
	account.Proxy = nil
	if account.Extra != nil {
		delete(account.Extra, UpstreamBillingProbeExtraKey)
	}
	if err := s.accountRepo.Update(ctx, account); err != nil {
		return nil, err
	}
	if err := propagateAccountProxyToShadows(ctx, s.accountRepo, accountID, account.ProxyID); err != nil {
		return nil, err
	}
	if err := s.repo.LockBinding(ctx, accountID, chosen.PoolID); err != nil {
		return nil, err
	}
	if err := s.resumeIfPaused(ctx, accountID); err != nil {
		return nil, err
	}
	p := chosen.Proxy
	return &p, nil
}

// ResumeAccount 人工确认落地 IP 已恢复：解除故障暂停、恢复调度，不换 IP。
func (s *ProxyPoolService) ResumeAccount(ctx context.Context, accountID int64) error {
	repo, err := s.incidentRepo()
	if err != nil {
		return err
	}
	rec, err := repo.GetPausedIncidentAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if rec == nil {
		return ErrProxyIncidentNotPaused
	}
	return s.resumeIfPaused(ctx, accountID)
}

// resumeIfPaused 若账号是被落地 IP 故障暂停的，则恢复调度并标记处置记录已处理；其他账号只标记记录。
func (s *ProxyPoolService) resumeIfPaused(ctx context.Context, accountID int64) error {
	repo, err := s.incidentRepo()
	if err != nil {
		return nil
	}
	rec, err := repo.GetPausedIncidentAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if rec != nil {
		if err := s.accountRepo.SetSchedulable(ctx, accountID, true); err != nil {
			return err
		}
	}
	return repo.MarkAccountResumed(ctx, accountID)
}

// ProxyMigrateResult 是批量迁移的单账号结果。
type ProxyMigrateResult struct {
	AccountID int64
	ToProxyID *int64
	Error     string
}

// MigrateProxyAccounts 把挂在某落地代理上的账号逐个换到其他可用 IP（各自按平台策略选），失败的保留原状。
func (s *ProxyPoolService) MigrateProxyAccounts(ctx context.Context, proxyID int64) ([]ProxyMigrateResult, error) {
	repo, err := s.incidentRepo()
	if err != nil {
		return nil, err
	}
	ids, err := repo.ListProxyAccountIDs(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	out := make([]ProxyMigrateResult, 0, len(ids))
	for _, id := range ids {
		res := ProxyMigrateResult{AccountID: id}
		if p, err := s.ReplaceAccountProxy(ctx, id, nil, proxyID); err != nil {
			res.Error = infraerrors.Reason(err)
			if res.Error == "" {
				res.Error = err.Error()
			}
		} else {
			res.ToProxyID = &p.ID
		}
		out = append(out, res)
	}
	return out, nil
}

// CheckProxyHealth 立即巡检一个落地代理（管理端手动触发，便于确认恢复），返回当前未恢复的故障。
func (s *ProxyPoolService) CheckProxyHealth(ctx context.Context, proxyID int64) (*ProxyEgressIncident, error) {
	v, ok := activeProxyEgressHealthService.Load("svc")
	if !ok {
		return nil, ErrProxyHealthUnavailable
	}
	health := v.(*ProxyEgressHealthService)
	p, err := health.proxyRepo.GetByID(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	if NormalizeProxyRole(p.Role) != ProxyRoleEgress {
		return nil, ErrProxyPoolTransportProxy
	}
	if err := health.CheckProxy(ctx, p); err != nil {
		return nil, err
	}
	return health.incidents.GetOpenIncident(ctx, proxyID)
}

// GetEgressHealthConfig 返回当前巡检配置（未设置时为默认值）。
func (s *ProxyPoolService) GetEgressHealthConfig(ctx context.Context) (ProxyEgressHealthConfig, error) {
	v, ok := activeProxyEgressHealthService.Load("svc")
	if !ok {
		return ProxyEgressHealthConfig{}, ErrProxyHealthUnavailable
	}
	return v.(*ProxyEgressHealthService).config(ctx), nil
}

// UpdateEgressHealthConfig 保存巡检配置，下一个调度周期生效（无需重启）。
func (s *ProxyPoolService) UpdateEgressHealthConfig(ctx context.Context, cfg ProxyEgressHealthConfig) (ProxyEgressHealthConfig, error) {
	if err := validateProxyEgressHealthConfig(cfg); err != nil {
		return cfg, err
	}
	v, ok := activeProxyEgressHealthService.Load("svc")
	if !ok || v.(*ProxyEgressHealthService).settings == nil {
		return cfg, ErrProxyHealthUnavailable
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	if err := v.(*ProxyEgressHealthService).settings.Set(ctx, SettingKeyProxyEgressHealth, string(raw)); err != nil {
		return cfg, err
	}
	return cfg, nil
}

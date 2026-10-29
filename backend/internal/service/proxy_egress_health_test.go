//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// healthAccountRepo 只实现巡检/换 IP 用到的账号方法。
type healthAccountRepo struct {
	accountRepoStub
	accounts map[int64]*Account
	paused   map[int64]bool
}

func (r *healthAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	a, ok := r.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	cp := *a
	return &cp, nil
}

func (r *healthAccountRepo) Update(_ context.Context, a *Account) error {
	cp := *a
	r.accounts[a.ID] = &cp
	return nil
}

func (r *healthAccountRepo) SetSchedulable(_ context.Context, id int64, v bool) error {
	r.accounts[id].Schedulable = v
	r.paused[id] = !v
	return nil
}

func (r *healthAccountRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}

// healthPoolRepo 在 proxyPoolRepoStub 基础上实现内存版故障记录。
type healthPoolRepo struct {
	proxyPoolRepoStub
	accountRepo *healthAccountRepo
	incidents   map[int64]*ProxyEgressIncident // proxy id -> 未恢复故障
	records     []ProxyEgressIncidentAccount
	nextID      int64
}

func (r *healthPoolRepo) ListPools(context.Context) ([]ProxyPool, error) {
	out := []ProxyPool{}
	for _, p := range r.pools {
		out = append(out, *p)
	}
	return out, nil
}

func (r *healthPoolRepo) ListProxyAccountIDs(_ context.Context, proxyID int64) ([]int64, error) {
	out := []int64{}
	for id, a := range r.accountRepo.accounts {
		if a.ProxyID != nil && *a.ProxyID == proxyID {
			out = append(out, id)
		}
	}
	return out, nil
}

func (r *healthPoolRepo) ListOpenIncidentProxyIDs(context.Context) ([]int64, error) {
	out := []int64{}
	for id := range r.incidents {
		out = append(out, id)
	}
	return out, nil
}

func (r *healthPoolRepo) GetOpenIncident(_ context.Context, proxyID int64) (*ProxyEgressIncident, error) {
	return r.incidents[proxyID], nil
}

func (r *healthPoolRepo) RecordIncidentFailure(_ context.Context, proxyID int64, layer, msg string) (*ProxyEgressIncident, error) {
	in := r.incidents[proxyID]
	switch {
	case in == nil:
		r.nextID++
		in = &ProxyEgressIncident{ID: r.nextID, ProxyID: proxyID, Layer: layer, Status: ProxyIncidentStatusOpen, FailCount: 1}
		r.incidents[proxyID] = in
	case in.Layer != layer:
		in.Layer, in.FailCount, in.HandledAt = layer, 1, nil
	default:
		in.FailCount++
	}
	in.LastError = msg
	cp := *in
	return &cp, nil
}

func (r *healthPoolRepo) MarkIncidentHandled(_ context.Context, id int64, _ *int64) error {
	now := time.Now()
	for _, in := range r.incidents {
		if in.ID == id {
			in.HandledAt = &now
		}
	}
	return nil
}

func (r *healthPoolRepo) ResolveIncident(_ context.Context, id int64) error {
	for pid, in := range r.incidents {
		if in.ID == id {
			delete(r.incidents, pid)
		}
	}
	return nil
}

func (r *healthPoolRepo) RecordIncidentAccount(_ context.Context, rec *ProxyEgressIncidentAccount) error {
	r.records = append(r.records, *rec)
	return nil
}

func (r *healthPoolRepo) GetPausedIncidentAccount(_ context.Context, accountID int64) (*ProxyEgressIncidentAccount, error) {
	for i := len(r.records) - 1; i >= 0; i-- {
		rec := r.records[i]
		if rec.AccountID == accountID && rec.Action == ProxyIncidentActionPaused && rec.ResumedAt == nil {
			return &rec, nil
		}
	}
	return nil, nil
}

func (r *healthPoolRepo) MarkAccountResumed(_ context.Context, accountID int64) error {
	now := time.Now()
	for i := range r.records {
		if r.records[i].AccountID == accountID {
			r.records[i].ResumedAt = &now
		}
	}
	return nil
}

func (r *healthPoolRepo) ListIncidents(context.Context, int) ([]ProxyEgressIncident, error) {
	return nil, nil
}

type fakeEgressProber struct{ down map[string]bool }

func (p *fakeEgressProber) ProbeProxy(_ context.Context, url string) (*ProxyExitInfo, int64, error) {
	if p.down[url] {
		return nil, 0, errors.New("connection refused")
	}
	return &ProxyExitInfo{IP: "203.0.113.1"}, 10, nil
}

func healthFixture(t *testing.T) (*ProxyEgressHealthService, *healthPoolRepo, *healthAccountRepo, *fakeEgressProber) {
	t.Helper()
	bad := Proxy{ID: 1, Name: "bad", Protocol: "socks5", Host: "10.0.0.1", Port: 1, Status: StatusActive}
	good := Proxy{ID: 2, Name: "good", Protocol: "socks5", Host: "10.0.0.2", Port: 1, Status: StatusActive}
	one := int64(1)
	accRepo := &healthAccountRepo{
		accounts: map[int64]*Account{
			10: {ID: 10, Platform: "kiro", ProxyID: &one, Schedulable: true},
			11: {ID: 11, Platform: "opencode_go", ProxyID: &one, Schedulable: true},
			12: {ID: 12, Platform: "opencode_go", ProxyID: &one, Schedulable: false},
		},
		paused: map[int64]bool{},
	}
	poolRepo := &healthPoolRepo{
		proxyPoolRepoStub: proxyPoolRepoStub{
			pools: map[int64]*ProxyPool{1: {ID: 1, Name: "default", IsDefault: true}},
			policies: []ProxyIPPolicy{
				{Platform: "kiro", Mode: ProxyIPPolicyModeChurn, OnEgressFailure: ProxyIPFailureReassign},
				{Platform: "opencode_go", Mode: ProxyIPPolicyModeSticky, OnEgressFailure: ProxyIPFailurePause},
			},
			members: []ProxyPoolMember{
				{Proxy: bad, HistoryCounts: map[string]int64{}, ActiveCounts: map[string]int64{}},
				{Proxy: good, HistoryCounts: map[string]int64{}, ActiveCounts: map[string]int64{}},
			},
		},
		accountRepo: accRepo,
		incidents:   map[int64]*ProxyEgressIncident{},
	}
	pool := NewProxyPoolService(poolRepo, accRepo)
	prev := activeProxyPoolService.Swap(pool)
	t.Cleanup(func() { activeProxyPoolService.Store(prev) })
	prober := &fakeEgressProber{down: map[string]bool{bad.URL(): true}}
	svc := NewProxyEgressHealthService(pool, poolRepo, nil, prober, nil, nil, nil, time.Minute)
	return svc, poolRepo, accRepo, prober
}

func TestProxyEgressHealth_ThresholdAndPolicyActions(t *testing.T) {
	svc, repo, accRepo, prober := healthFixture(t)
	ctx := context.Background()
	bad := repo.members[0].Proxy

	// 未到阈值：只记故障，不动账号。
	for i := 0; i < ProxyEgressDefaultFailThreshold-1; i++ {
		require.NoError(t, svc.CheckProxy(ctx, &bad))
	}
	require.Empty(t, repo.records)
	require.True(t, accRepo.accounts[11].Schedulable)

	require.NoError(t, svc.CheckProxy(ctx, &bad))
	actions := map[int64]string{}
	for _, r := range repo.records {
		actions[r.AccountID] = r.Action
	}
	require.Equal(t, ProxyIncidentActionReassigned, actions[10], "churn + reassign 自动换 IP")
	require.Equal(t, int64(2), *accRepo.accounts[10].ProxyID)
	require.Equal(t, ProxyIncidentActionPaused, actions[11], "sticky 暂停")
	require.False(t, accRepo.accounts[11].Schedulable)
	require.Equal(t, int64(1), *accRepo.accounts[11].ProxyID, "sticky 不换 IP")
	require.Equal(t, ProxyIncidentActionAlertOnly, actions[12], "原本不可调度的只告警")

	// 已处置：后续失败不重复处置。
	n := len(repo.records)
	require.NoError(t, svc.CheckProxy(ctx, &bad))
	require.Len(t, repo.records, n)

	// 探测恢复：故障关闭，但暂停的账号不自动恢复。
	prober.down = map[string]bool{}
	require.NoError(t, svc.CheckProxy(ctx, &bad))
	require.Empty(t, repo.incidents)
	require.False(t, accRepo.accounts[11].Schedulable)

	// 一键恢复：恢复调度、不换 IP；再次恢复报未暂停。
	require.NoError(t, svc.pool.ResumeAccount(ctx, 11))
	require.True(t, accRepo.accounts[11].Schedulable)
	require.Equal(t, int64(1), *accRepo.accounts[11].ProxyID)
	require.ErrorIs(t, svc.pool.ResumeAccount(ctx, 11), ErrProxyIncidentNotPaused)
}

// withUpstreamGroup 把 p 挂到一个单槽位前置组上，返回运行时与端点探测桩（测试结束后还原全局状态）。
func withUpstreamGroup(t *testing.T, p *Proxy) (*upstreamTestEnv, Proxy) {
	t.Helper()
	g := testUpstreamGroup(7, ProxyUpstreamSlot{ID: 70, Name: "s", EndpointIDs: []int64{1}})
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1))
	prevSvc := activeProxyChainService.Swap(&ProxyChainService{runtime: env.rt})
	gid := g.ID
	p.UpstreamMode, p.UpstreamGroupID = UpstreamModeGroup, &gid
	prevSnap := proxyChainState.Load()
	proxyChainState.Store(&proxyChainSnapshot{byID: map[int64]Proxy{p.ID: *p}, loaded: true})
	t.Cleanup(func() { proxyChainState.Store(prevSnap); activeProxyChainService.Store(prevSvc) })
	return env, *p
}

func TestProxyEgressHealth_TransportFailureDoesNotTouchAccounts(t *testing.T) {
	svc, repo, accRepo, _ := healthFixture(t)
	ctx := context.Background()
	p := repo.members[0].Proxy
	env, _ := withUpstreamGroup(t, &p)
	// 组内唯一槽位的端点挂了：整组没有可用槽位。
	env.probe.set(1, true)
	for i := 0; i < 3; i++ {
		env.tick(30 * time.Second)
	}
	require.False(t, env.rt.GroupAvailable(7))

	for i := 0; i < ProxyEgressDefaultFailThreshold; i++ {
		require.NoError(t, svc.CheckProxy(ctx, &p))
	}
	require.Equal(t, ProxyIncidentLayerTransport, repo.incidents[p.ID].Layer)
	require.NotNil(t, repo.incidents[p.ID].HandledAt)
	require.Empty(t, repo.records, "传输层故障只告警，不处置账号")
	require.True(t, accRepo.accounts[11].Schedulable)
	require.Equal(t, int64(1), *accRepo.accounts[10].ProxyID)
}

type recordingAlertSink struct{ events []OpsAlertEvent }

func (a *recordingAlertSink) IsMonitoringEnabled(context.Context) bool { return true }
func (a *recordingAlertSink) CreateAlertEvent(_ context.Context, e *OpsAlertEvent) (*OpsAlertEvent, error) {
	cp := *e
	cp.ID = int64(len(a.events) + 1)
	a.events = append(a.events, cp)
	return &cp, nil
}
func (a *recordingAlertSink) UpdateAlertEventStatus(context.Context, int64, string, *time.Time) error {
	return nil
}

// 整组没有可用槽位时，故障由槽位告警报出，落地事件只记录不再告警；只是经前置到落地不通时照常告警。
func TestProxyEgressHealth_GroupDownSkipsPerProxyAlert(t *testing.T) {
	svc, repo, _, _ := healthFixture(t)
	sink := &recordingAlertSink{}
	svc.alerts = sink
	p := repo.members[0].Proxy
	env, _ := withUpstreamGroup(t, &p)
	env.probe.set(1, true)
	for i := 0; i < 3; i++ {
		env.tick(30 * time.Second)
	}
	for range ProxyEgressDefaultFailThreshold {
		require.NoError(t, svc.CheckProxy(context.Background(), &p))
	}
	require.Equal(t, ProxyIncidentLayerTransport, repo.incidents[p.ID].Layer)
	require.NotNil(t, repo.incidents[p.ID].HandledAt)
	require.Empty(t, sink.events)

	// 组可用、只是经前置到该落地不通（直连可用）：按落地发 transport 告警。
	svc2, repo2, _, prober2 := healthFixture(t)
	sink2 := &recordingAlertSink{}
	svc2.alerts = sink2
	q := repo2.members[0].Proxy
	withUpstreamGroup(t, &q)
	prober2.down = map[string]bool{q.URL(): true}
	for range ProxyEgressDefaultFailThreshold {
		require.NoError(t, svc2.CheckProxy(context.Background(), &q))
	}
	require.Len(t, sink2.events, 1)
	require.Equal(t, "P2", sink2.events[0].Severity)
}

func TestProxyEgressHealth_LandingReachableDirectlyIsTransport(t *testing.T) {
	svc, repo, accRepo, prober := healthFixture(t)
	p := repo.members[0].Proxy
	withUpstreamGroup(t, &p)
	// 端点自身可用，但经它到该落地不通；落地直连可用。
	prober.down = map[string]bool{p.URL(): true}

	for range ProxyEgressDefaultFailThreshold {
		require.NoError(t, svc.CheckProxy(context.Background(), &p))
	}
	require.Equal(t, ProxyIncidentLayerTransport, repo.incidents[p.ID].Layer)
	require.Empty(t, repo.records)
	require.True(t, accRepo.accounts[11].Schedulable)

	// 直连也不通才是落地故障。
	prober.down[p.directURL()] = true
	for range ProxyEgressDefaultFailThreshold {
		require.NoError(t, svc.CheckProxy(context.Background(), &p))
	}
	require.Equal(t, ProxyIncidentLayerEgress, repo.incidents[p.ID].Layer)
	require.False(t, accRepo.accounts[11].Schedulable)
}

func TestReplaceAccountProxy(t *testing.T) {
	svc, repo, accRepo, _ := healthFixture(t)
	ctx := context.Background()

	cands, err := svc.pool.ReplaceCandidates(ctx, 11)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, int64(2), cands[0].Proxy.ID, "排除当前代理")

	// 有未恢复故障的代理不作为候选。
	repo.incidents[2] = &ProxyEgressIncident{ID: 99, ProxyID: 2}
	_, err = svc.pool.ReplaceAccountProxy(ctx, 11, nil, 0)
	require.ErrorIs(t, err, ErrProxyPoolNoAvailable)
	delete(repo.incidents, 2)

	// 指定不可用的目标：拒绝。
	bad := int64(1)
	_, err = svc.pool.ReplaceAccountProxy(ctx, 11, &bad, 0)
	require.ErrorIs(t, err, ErrProxyPoolProxyNotInPool)

	// 被故障暂停的账号换 IP 后恢复调度。
	accRepo.accounts[11].Schedulable = false
	repo.records = append(repo.records, ProxyEgressIncidentAccount{IncidentID: 1, AccountID: 11, Action: ProxyIncidentActionPaused})
	p, err := svc.pool.ReplaceAccountProxy(ctx, 11, nil, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), p.ID)
	require.Equal(t, int64(2), *accRepo.accounts[11].ProxyID)
	require.True(t, accRepo.accounts[11].Schedulable)
	require.Equal(t, []int64{11}, repo.lockLog)

	// 手动关掉调度、未被故障暂停的账号换 IP 不会被打开。
	accRepo.accounts[12].Schedulable = false
	_, err = svc.pool.ReplaceAccountProxy(ctx, 12, nil, 0)
	require.NoError(t, err)
	require.False(t, accRepo.accounts[12].Schedulable)
}

func TestMigrateProxyAccounts(t *testing.T) {
	svc, _, accRepo, _ := healthFixture(t)
	results, err := svc.pool.MigrateProxyAccounts(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, results, 3)
	for _, r := range results {
		require.Empty(t, r.Error)
		require.Equal(t, int64(2), *accRepo.accounts[r.AccountID].ProxyID)
	}
}

type healthProxyRepo struct {
	ProxyRepository
	active []ProxyWithAccountCount
}

func (r *healthProxyRepo) ListActiveWithAccountCount(context.Context) ([]ProxyWithAccountCount, error) {
	return r.active, nil
}

func (r *healthProxyRepo) GetByID(_ context.Context, id int64) (*Proxy, error) {
	for i := range r.active {
		if r.active[i].ID == id {
			p := r.active[i].Proxy
			return &p, nil
		}
	}
	return nil, ErrProxyNotFound
}

func TestProxyEgressHealth_CheckAllResolvesDeletedProxy(t *testing.T) {
	svc, repo, _, _ := healthFixture(t)
	good := repo.members[1].Proxy
	svc.proxyRepo = &healthProxyRepo{active: []ProxyWithAccountCount{{Proxy: good, AccountCount: 0}}}
	repo.incidents[99] = &ProxyEgressIncident{ID: 5, ProxyID: 99, Status: ProxyIncidentStatusOpen}
	require.NoError(t, svc.CheckAll(context.Background()))
	require.NotContains(t, repo.incidents, int64(99), "已删除代理的故障自动关闭")
}

type memSettingRepo struct {
	SettingRepository
	values map[string]string
}

func (r *memSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (r *memSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestProxyEgressHealthConfig(t *testing.T) {
	svc, repo, accRepo, _ := healthFixture(t)
	ctx := context.Background()
	settings := &memSettingRepo{values: map[string]string{}}
	svc.settings = settings
	activeProxyEgressHealthService.Store("svc", svc)
	t.Cleanup(func() { activeProxyEgressHealthService.Delete("svc") })

	cfg, err := svc.pool.GetEgressHealthConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, DefaultProxyEgressHealthConfig(), cfg, "未设置时用默认值")

	_, err = svc.pool.UpdateEgressHealthConfig(ctx, ProxyEgressHealthConfig{Enabled: true, IntervalSeconds: 10, FailThreshold: 3})
	require.ErrorIs(t, err, ErrProxyEgressConfigInvalid)
	_, err = svc.pool.UpdateEgressHealthConfig(ctx, ProxyEgressHealthConfig{Enabled: true, IntervalSeconds: 60, FailThreshold: 0})
	require.ErrorIs(t, err, ErrProxyEgressConfigInvalid)

	// 阈值改为 1：第一次失败即处置。
	_, err = svc.pool.UpdateEgressHealthConfig(ctx, ProxyEgressHealthConfig{Enabled: true, IntervalSeconds: 60, FailThreshold: 1})
	require.NoError(t, err)
	bad := repo.members[0].Proxy
	require.NoError(t, svc.CheckProxy(ctx, &bad))
	require.False(t, accRepo.accounts[11].Schedulable)

	// 损坏的配置回退默认值，不让巡检停掉。
	settings.values[SettingKeyProxyEgressHealth] = `{"interval_seconds":1}`
	require.Equal(t, DefaultProxyEgressHealthConfig(), svc.config(ctx))
}

func TestProxyEgressHealthRunOnceRespectsConfig(t *testing.T) {
	svc, _, _, _ := healthFixture(t)
	settings := &memSettingRepo{values: map[string]string{}}
	svc.settings = settings
	now := time.Unix(1_000_000, 0)
	svc.now = func() time.Time { return now }
	svc.proxyRepo = &healthProxyRepo{}

	settings.values[SettingKeyProxyEgressHealth] = `{"enabled":false,"interval_seconds":60,"fail_threshold":3}`
	svc.runOnce()
	require.True(t, svc.lastRun.IsZero(), "关闭时不巡检")

	settings.values[SettingKeyProxyEgressHealth] = `{"enabled":true,"interval_seconds":60,"fail_threshold":3}`
	svc.runOnce()
	require.Equal(t, now, svc.lastRun)
	first := now
	now = now.Add(30 * time.Second)
	svc.runOnce()
	require.Equal(t, first, svc.lastRun, "未到间隔不巡检")
	now = now.Add(31 * time.Second)
	svc.runOnce()
	require.Equal(t, now, svc.lastRun)
}

// 前置组没有可用槽位时，管理端测试直接报链路原因，不拨占位地址。
func TestProbeProxyChain_GroupDownReturnsReadableError(t *testing.T) {
	_, repo, _, prober := healthFixture(t)
	p := repo.members[1].Proxy
	env, _ := withUpstreamGroup(t, &p)
	env.probe.set(1, true)
	for i := 0; i < 3; i++ {
		env.tick(30 * time.Second)
	}
	svc := &adminServiceImpl{proxyProber: prober}
	_, _, err := svc.probeProxyChain(context.Background(), &p)
	require.ErrorContains(t, err, "upstream group has no available slot")
	require.NotContains(t, err.Error(), "proxychain-unavailable")
}

//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstreamProbe 按端点 id 返回预设结果，未设置的端点视为健康。
type fakeUpstreamProbe struct {
	mu   sync.Mutex
	down map[int64]bool
}

func (f *fakeUpstreamProbe) set(id int64, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down[id] = down
}

func (f *fakeUpstreamProbe) probe(_ context.Context, e ProxyUpstreamEndpoint, _ string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down[e.ID] {
		return 0, errors.New("probe failed")
	}
	return 10 * time.Millisecond, nil
}

type upstreamTestEnv struct {
	rt    *ProxyUpstreamRuntime
	probe *fakeUpstreamProbe
	now   time.Time
}

func (e *upstreamTestEnv) advance(d time.Duration) { e.now = e.now.Add(d) }

// tick 推进一个探测周期并探测一轮。
func (e *upstreamTestEnv) tick(interval time.Duration) {
	e.advance(interval)
	e.rt.ProbeDue(context.Background())
}

func upstreamEndpoints(ids ...int64) []ProxyUpstreamEndpoint {
	out := make([]ProxyUpstreamEndpoint, 0, len(ids))
	for _, id := range ids {
		out = append(out, ProxyUpstreamEndpoint{ID: id, Name: "ep" + string(rune('0'+id)), Protocol: "socks5", Host: "aio", Port: 17890 + int(id), Status: StatusActive})
	}
	return out
}

func newUpstreamTestEnv(t *testing.T, groups []ProxyUpstreamGroup, endpoints []ProxyUpstreamEndpoint) *upstreamTestEnv {
	t.Helper()
	env := &upstreamTestEnv{rt: NewProxyUpstreamRuntime(), probe: &fakeUpstreamProbe{down: map[int64]bool{}}, now: time.Unix(1_800_000_000, 0)}
	env.rt.probe = env.probe.probe
	env.rt.now = func() time.Time { return env.now }
	env.rt.Replace(groups, endpoints)
	env.rt.ProbeAll(context.Background())
	return env
}

func testUpstreamGroup(id int64, slots ...ProxyUpstreamSlot) ProxyUpstreamGroup {
	return ProxyUpstreamGroup{ID: id, Name: "g", Status: StatusActive, ProbeURL: ProxyUpstreamDefaultProbeURL,
		ProbeIntervalSeconds: 30, FailThreshold: 3, RecoverThreshold: 2, FailbackSeconds: 300, Slots: slots}
}

func currentEndpoint(t *testing.T, rt *ProxyUpstreamRuntime, slotID int64) int64 {
	t.Helper()
	st := rt.SlotRuntime(slotID)
	if st == nil || st.CurrentEndpointID == nil {
		return 0
	}
	return *st.CurrentEndpointID
}

func TestUpstreamSlotFailoverAndFailback(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "s1", EndpointIDs: []int64{1, 2}})
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1, 2))
	interval := 30 * time.Second
	if got := currentEndpoint(t, env.rt, 10); got != 1 {
		t.Fatalf("initial current = %d, want primary 1", got)
	}

	// 主端点失败未达阈值：不切换。
	env.probe.set(1, true)
	env.tick(interval)
	env.tick(interval)
	if got := currentEndpoint(t, env.rt, 10); got != 1 {
		t.Fatalf("below threshold current = %d, want 1", got)
	}
	// 第 3 次失败：切到备用。
	env.tick(interval)
	if got := currentEndpoint(t, env.rt, 10); got != 2 {
		t.Fatalf("after threshold current = %d, want backup 2", got)
	}
	if !env.rt.SlotRuntime(10).Available {
		t.Fatalf("slot with healthy backup must stay available")
	}

	// 主端点恢复：连续成功达到恢复阈值后仍要稳定满 failback 时长才切回。
	env.probe.set(1, false)
	env.tick(interval)
	env.tick(interval)
	if got := currentEndpoint(t, env.rt, 10); got != 2 {
		t.Fatalf("recovered but not stable: current = %d, want 2", got)
	}
	// 第 2 次成功（t+150s）判恢复；再过 300s（10 个周期）才稳定。
	for i := 0; i < 9; i++ {
		env.tick(interval)
	}
	if got := currentEndpoint(t, env.rt, 10); got != 2 {
		t.Fatalf("270s after recovery current = %d, want backup 2", got)
	}
	env.tick(interval)
	if got := currentEndpoint(t, env.rt, 10); got != 1 {
		t.Fatalf("after failback period current = %d, want primary 1", got)
	}
}

// 单次偶发失败由立即重试吸收，不计入连续失败。
func TestUpstreamProbeRetriesOnceBeforeCountingFailure(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "s1", EndpointIDs: []int64{1, 2}})
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1, 2))
	var calls int
	flaky := env.probe.probe
	env.rt.probe = func(ctx context.Context, e ProxyUpstreamEndpoint, u string) (time.Duration, error) {
		if e.ID == 1 {
			calls++
			if calls%2 == 1 {
				return 0, errors.New("EOF")
			}
		}
		return flaky(ctx, e, u)
	}
	for i := 0; i < 5; i++ {
		env.tick(30 * time.Second)
	}
	if got := currentEndpoint(t, env.rt, 10); got != 1 {
		t.Fatalf("flaky primary must not fail over: current = %d", got)
	}
	if h := env.rt.EndpointHealth(1); h.State != UpstreamHealthHealthy || h.ConsecutiveFailures != 0 {
		t.Fatalf("flaky primary health = %+v, want healthy", h)
	}
	if calls != 10 {
		t.Fatalf("probe calls = %d, want 10 (one retry per round)", calls)
	}
}

func TestUpstreamSlotWithSingleEndpointFailsClosed(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "only", EndpointIDs: []int64{1}})
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1))
	env.probe.set(1, true)
	for i := 0; i < 3; i++ {
		env.tick(30 * time.Second)
	}
	if env.rt.GroupAvailable(1) {
		t.Fatalf("group whose only slot is down must be unavailable")
	}
	if _, _, err := env.rt.SelectEndpoint(1, 42); err == nil {
		t.Fatalf("select must fail closed when no slot is available")
	}
}

func TestUpstreamSlotFailureReassignsOnlyAffectedProxies(t *testing.T) {
	g := testUpstreamGroup(1,
		ProxyUpstreamSlot{ID: 10, Name: "a", EndpointIDs: []int64{1}},
		ProxyUpstreamSlot{ID: 11, Name: "b", EndpointIDs: []int64{2}},
		ProxyUpstreamSlot{ID: 12, Name: "c", EndpointIDs: []int64{3}},
	)
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1, 2, 3))
	before := map[int64]int64{}
	perSlot := map[int64]int{}
	for pid := int64(1); pid <= 300; pid++ {
		_, slot, err := env.rt.SelectEndpoint(1, pid)
		if err != nil {
			t.Fatal(err)
		}
		before[pid] = slot.ID
		perSlot[slot.ID]++
	}
	for id, n := range perSlot {
		if n < 60 {
			t.Fatalf("slot %d got %d of 300 proxies; rendezvous hash should spread evenly (%v)", id, n, perSlot)
		}
	}

	// 槽位 b 的唯一端点挂了：只有原本在 b 的落地改派，其余不动。
	env.probe.set(2, true)
	for i := 0; i < 3; i++ {
		env.tick(30 * time.Second)
	}
	if !env.rt.GroupAvailable(1) {
		t.Fatalf("group must stay available with remaining slots")
	}
	for pid, prev := range before {
		_, slot, err := env.rt.SelectEndpoint(1, pid)
		if err != nil {
			t.Fatal(err)
		}
		if prev != 11 && slot.ID != prev {
			t.Fatalf("proxy %d moved from healthy slot %d to %d", pid, prev, slot.ID)
		}
		if prev == 11 && slot.ID == 11 {
			t.Fatalf("proxy %d still on failed slot", pid)
		}
	}

	// 槽位恢复后要稳定满 failback 时长才重新参与，之后落地回到原槽位。
	env.probe.set(2, false)
	env.tick(30 * time.Second)
	env.tick(30 * time.Second)
	if env.rt.SlotRuntime(11).Available {
		t.Fatalf("recovered slot must wait for failback period")
	}
	for i := 0; i < 10; i++ {
		env.tick(30 * time.Second)
	}
	for pid, prev := range before {
		if _, slot, _ := env.rt.SelectEndpoint(1, pid); slot.ID != prev {
			t.Fatalf("proxy %d did not return to slot %d after recovery (got %d)", pid, prev, slot.ID)
		}
	}
}

func TestUpstreamSharedEndpointAcrossSlots(t *testing.T) {
	// 端点 1 同时是槽位 a 的主、槽位 b 的备。
	g := testUpstreamGroup(1,
		ProxyUpstreamSlot{ID: 10, Name: "a", EndpointIDs: []int64{1}},
		ProxyUpstreamSlot{ID: 11, Name: "b", EndpointIDs: []int64{2, 1}},
	)
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1, 2))
	env.probe.set(2, true)
	for i := 0; i < 3; i++ {
		env.tick(30 * time.Second)
	}
	if got := currentEndpoint(t, env.rt, 11); got != 1 {
		t.Fatalf("slot b current = %d, want shared backup 1", got)
	}
	if got := currentEndpoint(t, env.rt, 10); got != 1 {
		t.Fatalf("slot a current = %d, want 1", got)
	}
}

func TestUpstreamInactiveEndpointIsSkipped(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "s", EndpointIDs: []int64{1, 2}})
	eps := upstreamEndpoints(1, 2)
	eps[0].Status = "inactive"
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, eps)
	if got := currentEndpoint(t, env.rt, 10); got != 2 {
		t.Fatalf("inactive primary must be skipped, current = %d", got)
	}
}

func TestProxyURLUsesUpstreamGroup(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "s", EndpointIDs: []int64{1}})
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1))
	svc := &ProxyChainService{runtime: env.rt}
	activeProxyChainService.Store(svc)
	t.Cleanup(func() { activeProxyChainService.Store(nil); proxyChainState.Store(nil) })

	gid := int64(1)
	byID := map[int64]Proxy{
		2: {ID: 2, Name: "landing", Protocol: "socks5", Host: "1.2.3.4", Port: 1080, Status: StatusActive, UpstreamMode: UpstreamModeGroup, UpstreamGroupID: &gid},
		5: {ID: 5, Name: "plain", Protocol: "http", Host: "plain.example", Port: 80, Status: StatusActive},
	}

	// 快照未加载时，需要链路的代理 fail-closed。
	proxyChainState.Store(&proxyChainSnapshot{byID: byID})
	landing := byID[2]
	if u := landing.URL(); u != proxyChainInvalidURL {
		t.Fatalf("unloaded snapshot must fail closed, got %q", u)
	}
	proxyChainState.Store(&proxyChainSnapshot{byID: byID, loaded: true})

	// 调用方持有过期副本（如调度器缓存里仍是直连），以快照里的链配置为准。
	stale := Proxy{ID: 2, Name: "landing", Protocol: "socks5", Host: "1.2.3.4", Port: 1080, Status: StatusActive}
	if u := stale.URL(); !strings.HasPrefix(u, "socks5h://") || !strings.Contains(u, "@127.0.0.1:") {
		t.Fatalf("stale copy must follow snapshot chain, got %q", u)
	}
	plain := byID[5]
	if got := plain.URL(); got != "http://plain.example:80" {
		t.Fatalf("plain proxy URL changed: %q", got)
	}
	hops, errMsg := DescribeProxyChain(&landing)
	if errMsg != "" || len(hops) != 1 || hops[0].ID != 1 || hops[0].Name != "g/s/ep1" {
		t.Fatalf("describe: %v %q", hops, errMsg)
	}

	// 前置组缺失：fail-closed，不能悄悄直连。
	missing := Proxy{ID: 7, Name: "m", Protocol: "socks5", Host: "5.6.7.8", Port: 1080, Status: StatusActive, UpstreamMode: UpstreamModeGroup, UpstreamGroupID: i64(99)}
	if u := missing.URL(); u != proxyChainInvalidURL {
		t.Fatalf("missing group must fail closed, got %q", u)
	}
	// group 模式但组 id 为空（组被删除后外键置空）：同样 fail-closed。
	orphan := Proxy{ID: 8, Name: "o", Protocol: "socks5", Host: "5.6.7.9", Port: 1080, Status: StatusActive, UpstreamMode: UpstreamModeGroup}
	if u := orphan.URL(); u != proxyChainInvalidURL {
		t.Fatalf("orphaned group mode must fail closed, got %q", u)
	}
}

func TestValidateProxyChainConfigModes(t *testing.T) {
	ctx := context.Background()
	p := &Proxy{Protocol: "socks5", Host: "h", Port: 1, UpstreamMode: "group"}
	if err := validateProxyChainConfig(ctx, p); err == nil {
		t.Fatalf("group mode without group id must be rejected")
	}
	p = &Proxy{Protocol: "socks5", Host: "h", Port: 1, UpstreamGroupID: i64(1)}
	if err := validateProxyChainConfig(ctx, p); err == nil {
		t.Fatalf("group id without group mode must be rejected")
	}
	// 旧模式值一律按直连处理（API 层已拒绝，这里防御存量数据）。
	if NormalizeUpstreamMode("hop_group") != UpstreamModeDirect || NormalizeUpstreamMode("default") != UpstreamModeDirect {
		t.Fatalf("legacy modes must normalize to direct")
	}
}

func TestValidateUpstreamGroup(t *testing.T) {
	base := func() *ProxyUpstreamGroup {
		g := testUpstreamGroup(0, ProxyUpstreamSlot{EndpointIDs: []int64{1}})
		g.Name = "x"
		return &g
	}
	g := base()
	if err := validateUpstreamGroup(g); err != nil || g.Slots[0].Name != "slot-1" {
		t.Fatalf("valid group: err=%v name=%q", err, g.Slots[0].Name)
	}
	g = base()
	g.Slots = nil
	if validateUpstreamGroup(g) == nil {
		t.Fatalf("group without slots must be rejected")
	}
	g = base()
	g.Slots[0].EndpointIDs = nil
	if validateUpstreamGroup(g) == nil {
		t.Fatalf("slot without endpoints must be rejected")
	}
	g = base()
	g.Slots[0].EndpointIDs = []int64{1, 1}
	if validateUpstreamGroup(g) == nil {
		t.Fatalf("duplicate endpoints in slot must be rejected")
	}
	g = base()
	g.ProbeURL = "ftp://x"
	if validateUpstreamGroup(g) == nil {
		t.Fatalf("non-HTTP probe URL must be rejected")
	}
	g = base()
	if err := validateUpstreamGroup(g); err != nil || g.Slots[0].Weight != ProxyUpstreamDefaultSlotWeight {
		t.Fatalf("omitted weight must default to 1: err=%v weight=%d", err, g.Slots[0].Weight)
	}
	for _, w := range []int{-1, ProxyUpstreamMaxSlotWeight + 1} {
		g = base()
		g.Slots[0].Weight = w
		if validateUpstreamGroup(g) == nil {
			t.Fatalf("weight %d must be rejected", w)
		}
	}
}

// 权重全为 1 时与原等权 rendezvous 选择一致：升级后存量落地不改派。
func TestUpstreamSlotEqualWeightMatchesUnweighted(t *testing.T) {
	slots := []int64{10, 11, 12}
	for pid := int64(1); pid <= 2000; pid++ {
		var unweighted, weighted int64
		var bu uint64
		var bw float64
		for _, sid := range slots {
			if u := rendezvousScore(pid, sid); unweighted == 0 || u > bu {
				unweighted, bu = sid, u
			}
			if w := weightedRendezvousScore(pid, sid, 1); weighted == 0 || w > bw {
				weighted, bw = sid, w
			}
		}
		if unweighted != weighted {
			t.Fatalf("proxy %d: weighted=1 picked %d, unweighted picked %d", pid, weighted, unweighted)
		}
	}
}

// 落地按权重比例分到各槽位；只调高一个槽位的权重时，落地只会移入该槽位。
func TestUpstreamSlotWeightDistribution(t *testing.T) {
	build := func(wa int) ProxyUpstreamGroup {
		return testUpstreamGroup(1,
			ProxyUpstreamSlot{ID: 10, Name: "a", Weight: wa, EndpointIDs: []int64{1}},
			ProxyUpstreamSlot{ID: 11, Name: "b", Weight: 1, EndpointIDs: []int64{2}},
		)
	}
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{build(1)}, upstreamEndpoints(1, 2))
	const n = 3000
	before := map[int64]int64{}
	for pid := int64(1); pid <= n; pid++ {
		_, slot, err := env.rt.SelectEndpoint(1, pid)
		if err != nil {
			t.Fatal(err)
		}
		before[pid] = slot.ID
	}
	env.rt.Replace([]ProxyUpstreamGroup{build(3)}, upstreamEndpoints(1, 2))
	env.rt.ProbeAll(context.Background())
	onA := 0
	for pid := int64(1); pid <= n; pid++ {
		_, slot, err := env.rt.SelectEndpoint(1, pid)
		if err != nil {
			t.Fatal(err)
		}
		if slot.ID == 10 {
			onA++
		}
		if before[pid] == 10 && slot.ID != 10 {
			t.Fatalf("proxy %d left slot a after its weight increased", pid)
		}
	}
	// 权重 3:1，期望约 75%。
	if ratio := float64(onA) / n; ratio < 0.71 || ratio > 0.79 {
		t.Fatalf("slot a share = %.3f, want ~0.75", ratio)
	}
}

// AIO 停用时其下端口一并跳过，切到其他 AIO 的备用端口。
func TestUpstreamInactiveAIOSkipsItsPorts(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "s", EndpointIDs: []int64{1, 2}})
	eps := upstreamEndpoints(1, 2)
	eps[0].AIOID, eps[0].AIOStatus = 1, "inactive"
	eps[1].AIOID, eps[1].AIOStatus = 2, StatusActive
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, eps)
	if got := currentEndpoint(t, env.rt, 10); got != 2 {
		t.Fatalf("port of inactive AIO must be skipped, current = %d", got)
	}
}

func TestValidateUpstreamAIOAndEndpoint(t *testing.T) {
	a := &ProxyUpstreamAIO{Name: " aio ", Protocol: "SOCKS5", Host: " 10.0.0.1 "}
	if err := validateUpstreamAIO(a); err != nil || a.Name != "aio" || a.Protocol != "socks5" || a.Host != "10.0.0.1" || a.Status != StatusActive {
		t.Fatalf("valid AIO: err=%v %+v", err, a)
	}
	for _, bad := range []ProxyUpstreamAIO{
		{Name: "", Protocol: "socks5", Host: "h"},
		{Name: "x", Protocol: "vmess", Host: "h"},
		{Name: "x", Protocol: "http", Host: "a/b"},
		{Name: "x", Protocol: "http", Host: "h", Status: "paused"},
	} {
		bad := bad
		if validateUpstreamAIO(&bad) == nil {
			t.Fatalf("AIO %+v must be rejected", bad)
		}
	}
	e := &ProxyUpstreamEndpoint{AIOID: 1, Name: " xyy ", Port: 17911}
	if err := validateUpstreamEndpoint(e); err != nil || e.Name != "xyy" || e.Status != StatusActive {
		t.Fatalf("valid port: err=%v %+v", err, e)
	}
	for _, bad := range []ProxyUpstreamEndpoint{
		{Name: "xyy", Port: 17911},
		{AIOID: 1, Name: "", Port: 17911},
		{AIOID: 1, Name: "xyy", Port: 0},
		{AIOID: 1, Name: "xyy", Port: 70000},
	} {
		bad := bad
		if validateUpstreamEndpoint(&bad) == nil {
			t.Fatalf("port %+v must be rejected", bad)
		}
	}
}

func TestUpstreamStartupReadinessWaitsForFirstProbe(t *testing.T) {
	rt := NewProxyUpstreamRuntime()
	probe := &fakeUpstreamProbe{down: map[int64]bool{}}
	rt.probe = probe.probe
	if ready, loaded, _ := rt.StartupReadiness(); ready || loaded {
		t.Fatalf("before config load: ready=%v loaded=%v, want false/false", ready, loaded)
	}

	inactive := testUpstreamGroup(2, ProxyUpstreamSlot{ID: 20, EndpointIDs: []int64{3}})
	inactive.Status = StatusDisabled
	rt.Replace([]ProxyUpstreamGroup{
		testUpstreamGroup(1, ProxyUpstreamSlot{ID: 11, EndpointIDs: []int64{1}}, ProxyUpstreamSlot{ID: 10, EndpointIDs: []int64{2}}),
		inactive,
	}, upstreamEndpoints(1, 2, 3))
	ready, loaded, pending := rt.StartupReadiness()
	if ready || !loaded || len(pending) != 2 || pending[0].SlotID != 10 || pending[1].SlotID != 11 {
		t.Fatalf("before probe: ready=%v loaded=%v pending=%+v, want 2 pending slots of group 1 sorted", ready, loaded, pending)
	}

	// 探测结果不论成败都算完成。
	probe.set(1, true)
	rt.ProbeAll(context.Background())
	if ready, _, pending := rt.StartupReadiness(); !ready || len(pending) != 0 {
		t.Fatalf("after probe: ready=%v pending=%+v, want ready", ready, pending)
	}

	// 锁存：就绪后新增的槽位不让实例回到未就绪。
	rt.Replace([]ProxyUpstreamGroup{
		testUpstreamGroup(1, ProxyUpstreamSlot{ID: 11, EndpointIDs: []int64{1}}, ProxyUpstreamSlot{ID: 10, EndpointIDs: []int64{2}}),
		testUpstreamGroup(3, ProxyUpstreamSlot{ID: 30, EndpointIDs: []int64{4}}),
	}, upstreamEndpoints(1, 2, 4))
	if ready, _, _ := rt.StartupReadiness(); !ready {
		t.Fatalf("readiness must latch after first probe round")
	}
}

func TestUpstreamStartupReadinessWithoutGroups(t *testing.T) {
	rt := NewProxyUpstreamRuntime()
	rt.Replace(nil, nil)
	if ready, loaded, pending := rt.StartupReadiness(); !ready || !loaded || len(pending) != 0 {
		t.Fatalf("no groups: ready=%v loaded=%v pending=%+v, want ready", ready, loaded, pending)
	}
	// 槽位里没有启用的端点可探：不阻塞就绪。
	rt = NewProxyUpstreamRuntime()
	ep := upstreamEndpoints(1)
	ep[0].Status = StatusDisabled
	rt.Replace([]ProxyUpstreamGroup{testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, EndpointIDs: []int64{1}})}, ep)
	if ready, _, _ := rt.StartupReadiness(); !ready {
		t.Fatalf("slot without active endpoints must not block readiness")
	}
}

func TestUpstreamStartupFirstRoundFailureMarksUnknownDown(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "s1", EndpointIDs: []int64{1, 2}})
	rt := NewProxyUpstreamRuntime()
	probe := &fakeUpstreamProbe{down: map[int64]bool{1: true}}
	now := time.Unix(1_800_000_000, 0)
	rt.probe = probe.probe
	rt.now = func() time.Time { return now }
	rt.Replace([]ProxyUpstreamGroup{g}, upstreamEndpoints(1, 2))

	// 启动首轮：主端点本轮含重试仍失败，直接判不可用，切到备用（不等 fail_threshold=3）。
	rt.ProbeAll(context.Background())
	if got := currentEndpoint(t, rt, 10); got != 2 {
		t.Fatalf("startup round current = %d, want backup 2", got)
	}
	if h := rt.EndpointHealth(1); h.State != UpstreamHealthUnhealthy {
		t.Fatalf("primary state = %s, want unhealthy", h.State)
	}
	// 恢复走正常的 recover_threshold。
	probe.set(1, false)
	now = now.Add(30 * time.Second)
	rt.ProbeDue(context.Background())
	if h := rt.EndpointHealth(1); h.State != UpstreamHealthUnhealthy {
		t.Fatalf("after 1 success state = %s, want still unhealthy (recover_threshold=2)", h.State)
	}
	now = now.Add(30 * time.Second)
	rt.ProbeDue(context.Background())
	if h := rt.EndpointHealth(1); h.State != UpstreamHealthHealthy {
		t.Fatalf("after 2 successes state = %s, want healthy", h.State)
	}
}

func TestUpstreamAfterReadyUnknownFailureUsesThreshold(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, Name: "s1", EndpointIDs: []int64{1}})
	env := newUpstreamTestEnv(t, []ProxyUpstreamGroup{g}, upstreamEndpoints(1))
	if ready, _, _ := env.rt.StartupReadiness(); !ready {
		t.Fatalf("want ready after initial probe")
	}
	// 就绪后新增的组：状态未知端点首轮失败仍按 fail_threshold 计数，不立即判不可用。
	g2 := testUpstreamGroup(2, ProxyUpstreamSlot{ID: 20, Name: "s2", EndpointIDs: []int64{2, 3}})
	env.rt.Replace([]ProxyUpstreamGroup{g, g2}, upstreamEndpoints(1, 2, 3))
	env.probe.set(2, true)
	env.rt.ProbeAll(context.Background())
	if got := currentEndpoint(t, env.rt, 20); got != 2 {
		t.Fatalf("after-ready first failure current = %d, want primary 2 (below threshold)", got)
	}
	env.tick(30 * time.Second)
	env.tick(30 * time.Second)
	if got := currentEndpoint(t, env.rt, 20); got != 3 {
		t.Fatalf("after threshold current = %d, want backup 3", got)
	}
}

func TestUpstreamStartupTransientFailureRecoveredByRetry(t *testing.T) {
	g := testUpstreamGroup(1, ProxyUpstreamSlot{ID: 10, EndpointIDs: []int64{1, 2}})
	rt := NewProxyUpstreamRuntime()
	calls := 0
	rt.probe = func(_ context.Context, e ProxyUpstreamEndpoint, _ string) (time.Duration, error) {
		if e.ID == 1 {
			calls++
			if calls == 1 {
				return 0, errors.New("eof")
			}
		}
		return time.Millisecond, nil
	}
	rt.Replace([]ProxyUpstreamGroup{g}, upstreamEndpoints(1, 2))
	rt.ProbeAll(context.Background())
	if got := currentEndpoint(t, rt, 10); got != 1 {
		t.Fatalf("transient failure fixed by retry: current = %d, want primary 1", got)
	}
}

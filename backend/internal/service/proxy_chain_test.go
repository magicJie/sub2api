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
}

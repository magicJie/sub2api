package service

// 链式代理（fork）：前置组运行时，见 docs/PROXY_IP_MODEL.md 第 2.2 节。
//
// 每个实例在内存中独立维护：
//   - 端点健康：经端点请求组的 probe_url（单次失败立即重试一次），连续失败 fail_threshold 次判不可用，连续成功 recover_threshold 次判恢复。
//     同一端点被多个组引用时按组参数分别计数（不同组的探测地址可能不同）。
//   - 槽位当前端点：当前端点不可用时切到列表中下一个可用端点；更靠前的端点连续健康满 failback_seconds 后切回。
//   - 槽位可用：至少一个端点可用。不可用的槽位从选择中剔除，恢复后也要连续可用满 failback_seconds 才重新参与，
//     避免落地在槽位间来回改派。
//   - 落地选槽：加权 rendezvous hash（落地 id × 槽位 id，按槽位权重缩放）在可用槽位中取最大者，不存状态；
//     落地按权重比例分到各槽位，增删槽位或改权重只改派受影响的那部分落地。
//
// 运行时只影响「新连接走哪个端点」：切换后 ProxyChainService 刷新快照，旧链由 proxychain.Manager.Retain 回收。
//
// 启动就绪（/ready）：所有启用组的每个槽位都探测过一轮才算就绪，就绪后锁存不再回落。
// 就绪前（启动首轮）状态未知的端点本轮含重试仍失败，直接判不可用，不等 fail_threshold；就绪后恢复正常计数。

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
)

const (
	proxyUpstreamProbeTimeout = 10 * time.Second
	proxyUpstreamTick         = 5 * time.Second
	proxyUpstreamMaxProbes    = 16 // 同时进行的端点探测数
)

// 端点健康状态。
const (
	UpstreamHealthUnknown   = "unknown"
	UpstreamHealthHealthy   = "healthy"
	UpstreamHealthUnhealthy = "unhealthy"
)

// UpstreamEndpointHealth 是端点在某个组参数下的健康状态（展示用）。
type UpstreamEndpointHealth struct {
	State                string     `json:"state"`
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	ConsecutiveSuccesses int        `json:"consecutive_successes"`
	HealthySince         *time.Time `json:"healthy_since,omitempty"`
	LastCheckedAt        *time.Time `json:"last_checked_at,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	LatencyMS            int64      `json:"latency_ms"`
}

// UpstreamSlotRuntime 是槽位的运行时状态（展示用）。
type UpstreamSlotRuntime struct {
	Available         bool       `json:"available"`
	CurrentEndpointID *int64     `json:"current_endpoint_id"`
	AvailableSince    *time.Time `json:"available_since,omitempty"`
}

// upstreamProbeFunc 经 endpoint 请求 probeURL，返回延迟；可在测试中替换。
type upstreamProbeFunc func(ctx context.Context, endpoint ProxyUpstreamEndpoint, probeURL string) (time.Duration, error)

type endpointHealthKey struct {
	groupID    int64
	endpointID int64
}

type endpointHealth struct {
	healthy      bool
	known        bool
	fails        int
	successes    int
	healthySince time.Time
	lastChecked  time.Time
	lastError    string
	latency      time.Duration
}

type slotState struct {
	current        int64 // 当前端点 id，0 表示无可用端点
	available      bool
	availableSince time.Time
	everAvailable  bool
}

// ProxyUpstreamRuntime 持有所有前置组的探测与切换状态。
type ProxyUpstreamRuntime struct {
	mu        sync.Mutex
	groups    map[int64]ProxyUpstreamGroup
	endpoints map[int64]ProxyUpstreamEndpoint
	health    map[endpointHealthKey]*endpointHealth
	slots     map[int64]*slotState // slot id -> state
	lastProbe map[int64]time.Time  // group id -> 上次探测时间
	probe     upstreamProbeFunc
	now       func() time.Time
	onChange  func() // 槽位当前端点或可用性变化时调用（刷新链路快照）
	loaded    bool   // 已加载过一次配置
	ready     bool   // 启动首轮探测已完成（锁存）

	probing sync.Mutex // 同一时刻只跑一轮探测
}

func NewProxyUpstreamRuntime() *ProxyUpstreamRuntime {
	return &ProxyUpstreamRuntime{
		groups: map[int64]ProxyUpstreamGroup{}, endpoints: map[int64]ProxyUpstreamEndpoint{},
		health: map[endpointHealthKey]*endpointHealth{}, slots: map[int64]*slotState{}, lastProbe: map[int64]time.Time{},
		probe: probeUpstreamEndpoint, now: time.Now,
	}
}

// Replace 用最新配置替换运行时；保留仍存在的端点健康与槽位状态。
// 新出现的槽位以主端点为当前端点、视为可用（探测出结果前不阻断流量，与启动时的行为一致）。
func (r *ProxyUpstreamRuntime) Replace(groups []ProxyUpstreamGroup, endpoints []ProxyUpstreamEndpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loaded = true
	r.groups = make(map[int64]ProxyUpstreamGroup, len(groups))
	r.endpoints = make(map[int64]ProxyUpstreamEndpoint, len(endpoints))
	for _, e := range endpoints {
		r.endpoints[e.ID] = e
	}
	keepHealth := map[endpointHealthKey]bool{}
	keepSlots := map[int64]bool{}
	for _, g := range groups {
		r.groups[g.ID] = g
		for _, slot := range g.Slots {
			keepSlots[slot.ID] = true
			for _, id := range slot.EndpointIDs {
				keepHealth[endpointHealthKey{g.ID, id}] = true
			}
			if _, ok := r.slots[slot.ID]; !ok {
				r.slots[slot.ID] = &slotState{}
			}
			r.reconcileSlotLocked(g, slot, r.now())
		}
	}
	for k := range r.health {
		if !keepHealth[k] {
			delete(r.health, k)
		}
	}
	for id := range r.slots {
		if !keepSlots[id] {
			delete(r.slots, id)
		}
	}
	for id := range r.lastProbe {
		if _, ok := r.groups[id]; !ok {
			delete(r.lastProbe, id)
		}
	}
}

// endpointUsableLocked：端点启用，且（未探测过或探测健康）。
func (r *ProxyUpstreamRuntime) endpointUsableLocked(groupID, endpointID int64) bool {
	e, ok := r.endpoints[endpointID]
	if !ok || !e.active() {
		return false
	}
	h := r.health[endpointHealthKey{groupID, endpointID}]
	return h == nil || !h.known || h.healthy
}

// endpointStableLocked：端点可用且已连续健康满 failback 时长（未探测过的不算稳定，不触发切回）。
func (r *ProxyUpstreamRuntime) endpointStableLocked(g ProxyUpstreamGroup, endpointID int64, now time.Time) bool {
	if !r.endpointUsableLocked(g.ID, endpointID) {
		return false
	}
	h := r.health[endpointHealthKey{g.ID, endpointID}]
	if h == nil || !h.known {
		return false
	}
	return now.Sub(h.healthySince) >= time.Duration(g.FailbackSeconds)*time.Second
}

// reconcileSlotLocked 按端点健康更新槽位当前端点与可用性，返回是否有变化。
func (r *ProxyUpstreamRuntime) reconcileSlotLocked(g ProxyUpstreamGroup, slot ProxyUpstreamSlot, now time.Time) bool {
	st := r.slots[slot.ID]
	prevCurrent, prevAvailable := st.current, st.available
	curIdx := -1
	for i, id := range slot.EndpointIDs {
		if id == st.current {
			curIdx = i
			break
		}
	}
	// 当前端点失效或已不在列表中：切到第一个可用端点。
	if curIdx < 0 || !r.endpointUsableLocked(g.ID, st.current) {
		st.current = 0
		for _, id := range slot.EndpointIDs {
			if r.endpointUsableLocked(g.ID, id) {
				st.current = id
				break
			}
		}
	} else {
		// failback：更靠前的端点稳定满 failback 时长后切回。
		for _, id := range slot.EndpointIDs[:curIdx] {
			if r.endpointStableLocked(g, id, now) {
				st.current = id
				break
			}
		}
	}
	hasEndpoint := st.current != 0
	switch {
	case !hasEndpoint:
		st.available = false
		st.availableSince = time.Time{}
	case !st.everAvailable:
		// 首次加载：直接可用，不等 failback。
		st.available, st.everAvailable, st.availableSince = true, true, now
	case !st.available:
		// 从故障恢复：当前端点稳定满 failback 时长后才重新参与选择。
		if st.availableSince.IsZero() {
			st.availableSince = now
		}
		if now.Sub(st.availableSince) >= time.Duration(g.FailbackSeconds)*time.Second {
			st.available = true
		}
	}
	return prevCurrent != st.current || prevAvailable != st.available
}

// SelectEndpoint 为落地选出前置端点：组内可用槽位按加权 rendezvous hash 取最大者，再取槽位当前端点。
func (r *ProxyUpstreamRuntime) SelectEndpoint(groupID, proxyID int64) (ProxyUpstreamEndpoint, ProxyUpstreamSlot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok {
		return ProxyUpstreamEndpoint{}, ProxyUpstreamSlot{}, errors.New("upstream group is missing")
	}
	if g.Status != StatusActive {
		return ProxyUpstreamEndpoint{}, ProxyUpstreamSlot{}, errors.New("upstream group is inactive")
	}
	var best *ProxyUpstreamSlot
	var bestScore float64
	for i := range g.Slots {
		slot := &g.Slots[i]
		st := r.slots[slot.ID]
		if st == nil || !st.available || st.current == 0 {
			continue
		}
		if score := weightedRendezvousScore(proxyID, slot.ID, slot.Weight); best == nil || score > bestScore {
			best, bestScore = slot, score
		}
	}
	if best == nil {
		return ProxyUpstreamEndpoint{}, ProxyUpstreamSlot{}, errors.New("upstream group has no available slot")
	}
	return r.endpoints[r.slots[best.ID].current], *best, nil
}

// weightedRendezvousScore 是加权 rendezvous hash 的得分 -w/ln(u)，u 为哈希映射到 (0,1) 的值。
// 各槽位胜出概率与权重成正比；权重相同时排序与 u 一致，即与等权 rendezvous 结果相同。
func weightedRendezvousScore(proxyID, slotID int64, weight int) float64 {
	if weight < 1 {
		weight = ProxyUpstreamDefaultSlotWeight
	}
	u := (float64(rendezvousScore(proxyID, slotID)>>11) + 0.5) / (1 << 53)
	return -float64(weight) / math.Log(u)
}

func rendezvousScore(proxyID, slotID int64) uint64 {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[:8], uint64(proxyID))
	binary.BigEndian.PutUint64(buf[8:], uint64(slotID))
	h := fnv.New64a()
	_, _ = h.Write(buf[:])
	// fnv 低位扩散差：再做一次 splitmix64 终混。
	x := h.Sum64()
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// GroupAvailable 报告组内是否还有可用槽位（巡检判层用）。
func (r *ProxyUpstreamRuntime) GroupAvailable(groupID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok || g.Status != StatusActive {
		return false
	}
	for _, slot := range g.Slots {
		if st := r.slots[slot.ID]; st != nil && st.available && st.current != 0 {
			return true
		}
	}
	return false
}

// GroupAllSlotsDown 报告启用中的组是否已没有可用槽位。此时槽位告警已覆盖故障，巡检不再按落地重复告警。
func (r *ProxyUpstreamRuntime) GroupAllSlotsDown(groupID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.groups[groupID]
	if !ok || g.Status != StatusActive || len(g.Slots) == 0 {
		return false
	}
	for _, slot := range g.Slots {
		if st := r.slots[slot.ID]; st != nil && st.available && st.current != 0 {
			return false
		}
	}
	return true
}

// SlotRuntime 返回槽位运行时状态。
func (r *ProxyUpstreamRuntime) SlotRuntime(slotID int64) *UpstreamSlotRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.slots[slotID]
	if st == nil {
		return nil
	}
	out := &UpstreamSlotRuntime{Available: st.available}
	if st.current != 0 {
		id := st.current
		out.CurrentEndpointID = &id
	}
	if !st.availableSince.IsZero() {
		t := st.availableSince
		out.AvailableSince = &t
	}
	return out
}

// EndpointHealth 汇总端点在各组下的健康：任一组判不可用即不可用，全部未探测为 unknown。
func (r *ProxyUpstreamRuntime) EndpointHealth(endpointID int64) UpstreamEndpointHealth {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := UpstreamEndpointHealth{State: UpstreamHealthUnknown}
	var latest time.Time
	for k, h := range r.health {
		if k.endpointID != endpointID || !h.known {
			continue
		}
		if !h.healthy {
			out.State = UpstreamHealthUnhealthy
		} else if out.State == UpstreamHealthUnknown {
			out.State = UpstreamHealthHealthy
		}
		if h.lastChecked.After(latest) {
			latest = h.lastChecked
			out.ConsecutiveFailures, out.ConsecutiveSuccesses = h.fails, h.successes
			out.LastError, out.LatencyMS = h.lastError, h.latency.Milliseconds()
			t := h.lastChecked
			out.LastCheckedAt = &t
			if h.healthy {
				s := h.healthySince
				out.HealthySince = &s
			} else {
				out.HealthySince = nil
			}
		}
	}
	return out
}

type upstreamProbeTask struct {
	group    ProxyUpstreamGroup
	endpoint ProxyUpstreamEndpoint
}

// ProbeDue 探测所有到期的组，返回是否有槽位变化。
func (r *ProxyUpstreamRuntime) ProbeDue(ctx context.Context) bool {
	return r.probeGroups(ctx, false)
}

// ProbeAll 立即探测全部组（启动、手动刷新）。
func (r *ProxyUpstreamRuntime) ProbeAll(ctx context.Context) bool {
	return r.probeGroups(ctx, true)
}

func (r *ProxyUpstreamRuntime) probeGroups(ctx context.Context, force bool) bool {
	r.probing.Lock()
	defer r.probing.Unlock()
	r.mu.Lock()
	now := r.now()
	var tasks []upstreamProbeTask
	for id, g := range r.groups {
		if g.Status != StatusActive {
			continue
		}
		if !force && now.Sub(r.lastProbe[id]) < time.Duration(g.ProbeIntervalSeconds)*time.Second {
			continue
		}
		r.lastProbe[id] = now
		seen := map[int64]bool{}
		for _, slot := range g.Slots {
			for _, eid := range slot.EndpointIDs {
				e, ok := r.endpoints[eid]
				if seen[eid] || !ok || !e.active() {
					continue
				}
				seen[eid] = true
				tasks = append(tasks, upstreamProbeTask{group: g, endpoint: e})
			}
		}
	}
	probe := r.probe
	r.mu.Unlock()
	if len(tasks) == 0 {
		return r.reconcileAll()
	}

	type result struct {
		task    upstreamProbeTask
		latency time.Duration
		err     error
		retried bool // 失败后已立即重试过一次（仍失败）
	}
	results := make([]result, len(tasks))
	sem := make(chan struct{}, proxyUpstreamMaxProbes)
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			lat, err := probeUpstreamOnce(ctx, probe, tasks[i])
			retried := false
			if err != nil && ctx.Err() == nil {
				// 单次失败立即重试一次：偶发的握手失败、EOF 不计入连续失败。
				lat, err = probeUpstreamOnce(ctx, probe, tasks[i])
				retried = true
			}
			results[i] = result{task: tasks[i], latency: lat, err: err, retried: retried}
		}(i)
	}
	wg.Wait()

	r.mu.Lock()
	now = r.now()
	startup := !r.ready
	for _, res := range results {
		k := endpointHealthKey{res.task.group.ID, res.task.endpoint.ID}
		h := r.health[k]
		if h == nil {
			h = &endpointHealth{}
			r.health[k] = h
		}
		r.recordProbeLocked(res.task.group, res.task.endpoint, h, res.latency, res.err, startup && res.retried, now)
	}
	r.startupPendingLocked()
	r.mu.Unlock()
	return r.reconcileAll()
}

func probeUpstreamOnce(ctx context.Context, probe upstreamProbeFunc, t upstreamProbeTask) (time.Duration, error) {
	pctx, cancel := context.WithTimeout(ctx, proxyUpstreamProbeTimeout)
	defer cancel()
	return probe(pctx, t.endpoint, t.group.ProbeURL)
}

// strictUnknown：启动首轮且本轮已重试仍失败时，状态未知的端点直接判不可用。
func (r *ProxyUpstreamRuntime) recordProbeLocked(g ProxyUpstreamGroup, e ProxyUpstreamEndpoint, h *endpointHealth, latency time.Duration, err error, strictUnknown bool, now time.Time) {
	h.lastChecked = now
	if err != nil {
		h.fails++
		h.successes = 0
		h.lastError = err.Error()
		if strictUnknown && !h.known {
			slog.Warn("proxy_upstream_endpoint_down_at_startup", "group_id", g.ID, "endpoint_id", e.ID, "endpoint", e.Name, "error", h.lastError)
			h.healthy, h.known = false, true
			return
		}
		// 首次探测失败也要达到阈值才判不可用；未知状态下仍视为可用。
		if h.fails >= g.FailThreshold && (h.healthy || !h.known) {
			if h.known {
				slog.Warn("proxy_upstream_endpoint_down", "group_id", g.ID, "endpoint_id", e.ID, "endpoint", e.Name, "error", h.lastError)
			}
			h.healthy, h.known = false, true
		}
		return
	}
	h.successes++
	h.fails = 0
	h.lastError = ""
	h.latency = latency
	if !h.known {
		// 首次成功即判健康；healthySince 设为现在，failback 计时从此开始。
		h.healthy, h.known, h.healthySince = true, true, now
		return
	}
	if !h.healthy && h.successes >= g.RecoverThreshold {
		slog.Info("proxy_upstream_endpoint_up", "group_id", g.ID, "endpoint_id", e.ID, "endpoint", e.Name)
		h.healthy, h.healthySince = true, now
	}
}

// UpstreamPendingSlot 是启动首轮尚未探测完的槽位（/ready 展示用）。
type UpstreamPendingSlot struct {
	GroupID int64 `json:"group_id"`
	SlotID  int64 `json:"slot_id"`
}

// StartupReadiness 报告启动首轮探测是否完成：配置已加载，且所有启用组的每个槽位里启用的端点都探测过（不论结果）。
// 没有启用的端点可探的槽位不阻塞就绪。一旦就绪即锁存，之后新增的组或槽位不会让实例回到未就绪。
// loaded 为 false 表示配置还没加载成功（此时 pending 为空）。
func (r *ProxyUpstreamRuntime) StartupReadiness() (ready, loaded bool, pending []UpstreamPendingSlot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending = r.startupPendingLocked()
	return r.ready, r.loaded, pending
}

// startupPendingLocked 返回未探测完的槽位，全部完成时锁存 ready。
func (r *ProxyUpstreamRuntime) startupPendingLocked() []UpstreamPendingSlot {
	if r.ready || !r.loaded {
		return nil
	}
	var pending []UpstreamPendingSlot
	for _, g := range r.groups {
		if g.Status != StatusActive {
			continue
		}
		for _, slot := range g.Slots {
			for _, eid := range slot.EndpointIDs {
				e, ok := r.endpoints[eid]
				if !ok || !e.active() {
					continue
				}
				if h := r.health[endpointHealthKey{g.ID, eid}]; h == nil || h.lastChecked.IsZero() {
					pending = append(pending, UpstreamPendingSlot{GroupID: g.ID, SlotID: slot.ID})
					break
				}
			}
		}
	}
	if len(pending) == 0 {
		r.ready = true
		return nil
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].GroupID != pending[j].GroupID {
			return pending[i].GroupID < pending[j].GroupID
		}
		return pending[i].SlotID < pending[j].SlotID
	})
	return pending
}

func (r *ProxyUpstreamRuntime) reconcileAll() bool {
	r.mu.Lock()
	now := r.now()
	changed := false
	for _, g := range r.groups {
		for _, slot := range g.Slots {
			if r.slots[slot.ID] == nil {
				r.slots[slot.ID] = &slotState{}
			}
			prev := *r.slots[slot.ID]
			if r.reconcileSlotLocked(g, slot, now) {
				changed = true
				st := r.slots[slot.ID]
				slog.Warn("proxy_upstream_slot_changed", "group_id", g.ID, "slot_id", slot.ID, "slot", slot.Name,
					"from_endpoint", prev.current, "to_endpoint", st.current, "available", st.available)
			}
		}
	}
	cb := r.onChange
	r.mu.Unlock()
	if changed && cb != nil {
		cb()
	}
	return changed
}

// probeUpstreamEndpoint 经端点请求 probeURL：2xx/3xx 视为健康。不跟随重定向，不复用连接。
func probeUpstreamEndpoint(ctx context.Context, e ProxyUpstreamEndpoint, probeURL string) (time.Duration, error) {
	u := &url.URL{Scheme: e.Protocol, Host: net.JoinHostPort(e.Host, strconv.Itoa(e.Port))}
	if e.Username != "" || e.Password != "" {
		u.User = url.UserPassword(e.Username, e.Password)
	}
	transport := &http.Transport{
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   proxyUpstreamProbeTimeout,
		ResponseHeaderTimeout: proxyUpstreamProbeTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext:           (&net.Dialer{Timeout: proxyUpstreamProbeTimeout}).DialContext,
	}
	if err := proxyutil.ConfigureTransportProxy(transport, u); err != nil {
		return 0, errors.New("invalid upstream endpoint")
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: proxyUpstreamProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return 0, errors.New("invalid probe URL")
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, sanitizeUpstreamProbeError(err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, errors.New("probe returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	return time.Since(start), nil
}

// sanitizeUpstreamProbeError 规整探测错误。代理拨号错误只含 host:port，不含端点凭据。
func sanitizeUpstreamProbeError(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("probe timed out")
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return errors.New(msg)
}

func (r *ProxyUpstreamRuntime) group(id int64) ProxyUpstreamGroup {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.groups[id]
}

func (r *ProxyUpstreamRuntime) groupIDByName(name string) *int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, g := range r.groups {
		if g.Name == name {
			v := id
			return &v
		}
	}
	return nil
}

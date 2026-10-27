package service

// Kiro（fork）额度后台刷新器（写法参照 ollama_cloud_usage.go 的 runner）。
//
//   - 只在 leader 实例上拉上游（tryAcquireSingletonLeaderLock）；所有实例每轮都从落库快照
//     恢复调度用的 kiroPaceSnapshots，多副本看到的是同一份额度。
//   - 处理 platform=kiro、type=oauth、未删除、status=active 的账号。
//   - 默认每 15 分钟一次；已用 ≥90% 或近 30 分钟有请求的账号缩短到 5 分钟；
//     每个账号的下次刷新时间按 (账号, 上次尝试时间) 做确定性抖动（±10%），避免扎堆。
//   - 失败按指数退避（1、2、4…分钟，上限 1 小时）；"全部刷新"写入的排队标记优先于退避。
//   - 全局并发默认 2；结果只写 extra.kiro_usage_snapshot，不触发调度 outbox / 缓存重建。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	// SettingKeyKiroUsageRefreshSettings 存放 Kiro 额度后台刷新的开关、间隔与并发。
	SettingKeyKiroUsageRefreshSettings = "kiro_usage_refresh_settings"

	kiroUsageRefreshDefaultIntervalMinutes = 15
	kiroUsageRefreshMinIntervalMinutes     = 5
	kiroUsageRefreshMaxIntervalMinutes     = 24 * 60
	kiroUsageRefreshDefaultConcurrency     = 2
	kiroUsageRefreshMinConcurrency         = 1
	kiroUsageRefreshMaxConcurrency         = 8

	// kiroUsageRefreshFastInterval 用于额度紧张（≥90%）或近期有请求的账号。
	kiroUsageRefreshFastInterval   = 5 * time.Minute
	kiroUsageRefreshHotUsedPercent = 90.0
	kiroUsageRefreshActiveWindow   = 30 * time.Minute
	kiroUsageRefreshJitterRatio    = 0.1
	kiroUsageRefreshBackoffBase    = time.Minute
	kiroUsageRefreshBackoffMax     = time.Hour
	// kiroUsageRefreshMinAttemptGap 防止落库失败时同一账号每轮都打上游。
	kiroUsageRefreshMinAttemptGap = time.Minute

	kiroUsageRefreshCycleInterval = time.Minute
	kiroUsageRefreshWakeMinGap    = 10 * time.Second
	kiroUsageRefreshMaxPerCycle   = 50
	kiroUsageRefreshCycleTimeout  = 4 * time.Minute
	kiroUsageRefreshLeaderLockKey = "kiro:usage:refresh:leader"
	kiroUsageRefreshLeaderLockTTL = 5 * time.Minute
)

var (
	ErrKiroUsageRefreshUnavailable = infraerrors.ServiceUnavailable(
		"KIRO_USAGE_REFRESH_UNAVAILABLE", "Kiro usage refresh is unavailable",
	)
	// ErrKiroUsageSnapshotStale 表示落库时快照已被别人改过（并发刷新或凭据变更清除），本次结果丢弃。
	ErrKiroUsageSnapshotStale = errors.New("kiro usage snapshot changed concurrently")
)

// KiroUsageRefreshSettings 是 Kiro 额度后台刷新的全局设置。
type KiroUsageRefreshSettings struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
	Concurrency     int  `json:"concurrency"`
}

func defaultKiroUsageRefreshSettings() *KiroUsageRefreshSettings {
	return &KiroUsageRefreshSettings{
		Enabled:         true,
		IntervalMinutes: kiroUsageRefreshDefaultIntervalMinutes,
		Concurrency:     kiroUsageRefreshDefaultConcurrency,
	}
}

func normalizeKiroUsageRefreshSettings(settings *KiroUsageRefreshSettings) {
	if settings.IntervalMinutes <= 0 {
		settings.IntervalMinutes = kiroUsageRefreshDefaultIntervalMinutes
	}
	settings.IntervalMinutes = min(max(settings.IntervalMinutes, kiroUsageRefreshMinIntervalMinutes), kiroUsageRefreshMaxIntervalMinutes)
	if settings.Concurrency <= 0 {
		settings.Concurrency = kiroUsageRefreshDefaultConcurrency
	}
	settings.Concurrency = min(max(settings.Concurrency, kiroUsageRefreshMinConcurrency), kiroUsageRefreshMaxConcurrency)
}

// GetKiroUsageRefreshSettings 读取设置；未配置时返回默认值（默认开启）。
func (s *SettingService) GetKiroUsageRefreshSettings(ctx context.Context) (*KiroUsageRefreshSettings, error) {
	defaults := defaultKiroUsageRefreshSettings()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyKiroUsageRefreshSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaults, nil
		}
		return nil, fmt.Errorf("get Kiro usage refresh settings: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return defaults, nil
	}
	settings := *defaults
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, fmt.Errorf("parse Kiro usage refresh settings: %w", err)
	}
	normalizeKiroUsageRefreshSettings(&settings)
	return &settings, nil
}

// SetKiroUsageRefreshSettings 校验并保存设置。
func (s *SettingService) SetKiroUsageRefreshSettings(ctx context.Context, settings *KiroUsageRefreshSettings) error {
	if s == nil || s.settingRepo == nil {
		return ErrKiroUsageRefreshUnavailable
	}
	if settings == nil {
		return infraerrors.BadRequest("INVALID_KIRO_USAGE_REFRESH_SETTINGS", "settings cannot be nil")
	}
	if settings.IntervalMinutes < kiroUsageRefreshMinIntervalMinutes || settings.IntervalMinutes > kiroUsageRefreshMaxIntervalMinutes {
		return infraerrors.BadRequest(
			"INVALID_KIRO_USAGE_REFRESH_INTERVAL",
			fmt.Sprintf("interval_minutes must be between %d and %d", kiroUsageRefreshMinIntervalMinutes, kiroUsageRefreshMaxIntervalMinutes),
		)
	}
	if settings.Concurrency < kiroUsageRefreshMinConcurrency || settings.Concurrency > kiroUsageRefreshMaxConcurrency {
		return infraerrors.BadRequest(
			"INVALID_KIRO_USAGE_REFRESH_CONCURRENCY",
			fmt.Sprintf("concurrency must be between %d and %d", kiroUsageRefreshMinConcurrency, kiroUsageRefreshMaxConcurrency),
		)
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal Kiro usage refresh settings: %w", err)
	}
	return s.settingRepo.Set(ctx, SettingKeyKiroUsageRefreshSettings, string(data))
}

// KiroUsageRefreshCandidate 是刷新器每轮扫描的轻量行（不含凭据）。
type KiroUsageRefreshCandidate struct {
	ID         int64
	LastUsedAt *time.Time
	Snapshot   *KiroUsageSnapshot
}

// KiroUsageSnapshotRepository 是刷新器需要的专用 repo 方法；只读写 extra.kiro_usage_snapshot。
type KiroUsageSnapshotRepository interface {
	// ListKiroUsageRefreshCandidates 返回 platform=kiro、type=oauth、未删除、status=active 的账号。
	ListKiroUsageRefreshCandidates(ctx context.Context) ([]KiroUsageRefreshCandidate, error)
	// UpdateKiroUsageSnapshot 只在 extra 里快照的 last_attempt_at 仍等于 expectedLastAttempt
	// （nil 表示没有快照）时写入，否则返回 ErrKiroUsageSnapshotStale。
	UpdateKiroUsageSnapshot(ctx context.Context, accountID int64, snapshot *KiroUsageSnapshot, expectedLastAttempt *time.Time) error
	// RequestKiroUsageRefreshAll 给所有候选账号的快照打上排队标记，返回受影响行数。
	RequestKiroUsageRefreshAll(ctx context.Context, at time.Time) (int64, error)
}

// KiroUsageRefreshService 在后台维护 Kiro 账号的额度快照。
type KiroUsageRefreshService struct {
	accountRepo    AccountRepository
	usageService   *AccountUsageService
	settingService *SettingService
	lockCache      LeaderLockCache
	db             *sql.DB
	instanceID     string
	now            func() time.Time
	// refreshFn 拉取并落库单个账号；测试可替换。
	refreshFn func(ctx context.Context, account *Account) error

	enabled     atomic.Bool
	concurrency atomic.Int32
	interval    atomic.Int64 // time.Duration

	parentCtx    context.Context
	parentCancel context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	started      bool
	stopped      bool
	cycleMu      sync.Mutex
	wake         chan struct{}
	attempts     sync.Map // accountID -> time.Time
}

// NewKiroUsageRefreshService 创建刷新器（不启动）。
func NewKiroUsageRefreshService(
	accountRepo AccountRepository,
	usageService *AccountUsageService,
	settingService *SettingService,
) *KiroUsageRefreshService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &KiroUsageRefreshService{
		accountRepo:    accountRepo,
		usageService:   usageService,
		settingService: settingService,
		instanceID:     uuid.NewString(),
		now:            time.Now,
		parentCtx:      ctx,
		parentCancel:   cancel,
		wake:           make(chan struct{}, 1),
	}
	s.applySettings(defaultKiroUsageRefreshSettings())
	s.refreshFn = s.refreshAccount
	if usageService != nil {
		usageService.kiroUsageRefresher = s
	}
	return s
}

// ProvideKiroUsageRefreshService 是 wire provider：注入到 AccountUsageService 并启动后台循环。
func ProvideKiroUsageRefreshService(
	accountRepo AccountRepository,
	usageService *AccountUsageService,
	settingService *SettingService,
	lockCache LeaderLockCache,
	db *sql.DB,
) *KiroUsageRefreshService {
	svc := NewKiroUsageRefreshService(accountRepo, usageService, settingService)
	svc.lockCache = lockCache
	svc.db = db
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := svc.reloadSettings(ctx); err != nil {
		logger.LegacyPrintf("service.kiro_usage_refresh", "load_settings_failed: err=%v", err)
	}
	svc.Start()
	return svc
}

func (s *KiroUsageRefreshService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go s.runLoop()
}

func (s *KiroUsageRefreshService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.parentCancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *KiroUsageRefreshService) runLoop() {
	defer s.wg.Done()
	s.runCycle()
	ticker := time.NewTicker(kiroUsageRefreshCycleInterval)
	defer ticker.Stop()
	var lastWake time.Time
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-ticker.C:
			s.runCycle()
		case <-s.wake:
			// 页面读到空快照会频繁唤醒，这里做最小间隔去抖。
			if gap := kiroUsageRefreshWakeMinGap - time.Since(lastWake); gap > 0 {
				select {
				case <-s.parentCtx.Done():
					return
				case <-time.After(gap):
				}
			}
			lastWake = time.Now()
			s.runCycle()
		}
	}
}

func (s *KiroUsageRefreshService) runCycle() {
	if _, err := s.reloadSettings(s.parentCtx); err != nil {
		logger.LegacyPrintf("service.kiro_usage_refresh", "load_settings_failed: err=%v", err)
	}
	if err := s.RunDue(s.parentCtx); err != nil && s.parentCtx.Err() == nil {
		logger.LegacyPrintf("service.kiro_usage_refresh", "run_due_failed: err=%v", err)
	}
}

// Wake 让后台尽快跑一轮（非阻塞，多次调用合并）。
func (s *KiroUsageRefreshService) Wake() {
	if s == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Enabled 报告后台刷新是否开启（取最近一次加载的设置，不查库）。
func (s *KiroUsageRefreshService) Enabled() bool {
	return s != nil && s.enabled.Load()
}

func (s *KiroUsageRefreshService) applySettings(settings *KiroUsageRefreshSettings) {
	s.enabled.Store(settings.Enabled)
	s.concurrency.Store(int32(settings.Concurrency))
	s.interval.Store(int64(time.Duration(settings.IntervalMinutes) * time.Minute))
}

func (s *KiroUsageRefreshService) reloadSettings(ctx context.Context) (*KiroUsageRefreshSettings, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.applySettings(settings)
	return settings, nil
}

func (s *KiroUsageRefreshService) GetSettings(ctx context.Context) (*KiroUsageRefreshSettings, error) {
	if s == nil || s.settingService == nil {
		return defaultKiroUsageRefreshSettings(), nil
	}
	return s.settingService.GetKiroUsageRefreshSettings(ctx)
}

func (s *KiroUsageRefreshService) UpdateSettings(ctx context.Context, settings *KiroUsageRefreshSettings) (*KiroUsageRefreshSettings, error) {
	if s == nil || s.settingService == nil {
		return nil, ErrKiroUsageRefreshUnavailable
	}
	if err := s.settingService.SetKiroUsageRefreshSettings(ctx, settings); err != nil {
		return nil, err
	}
	updated, err := s.reloadSettings(ctx)
	if err != nil {
		return nil, err
	}
	if updated.Enabled {
		s.Wake()
	}
	return updated, nil
}

// RequestRefreshAll 把所有 Kiro 账号排进后台队列并立即返回，由刷新器按并发上限慢慢跑。
func (s *KiroUsageRefreshService) RequestRefreshAll(ctx context.Context) (int64, error) {
	repo, ok := s.snapshotRepo()
	if !ok {
		return 0, ErrKiroUsageRefreshUnavailable
	}
	if !s.Enabled() {
		return 0, infraerrors.BadRequest("KIRO_USAGE_REFRESH_DISABLED", "Kiro usage background refresh is disabled")
	}
	// 没有快照的账号本来就会被立即刷新；有快照的打上排队标记。返回值是本次排队的账号总数。
	if _, err := repo.RequestKiroUsageRefreshAll(ctx, s.currentTime()); err != nil {
		return 0, err
	}
	candidates, err := repo.ListKiroUsageRefreshCandidates(ctx)
	if err != nil {
		return 0, err
	}
	s.Wake()
	return int64(len(candidates)), nil
}

func (s *KiroUsageRefreshService) snapshotRepo() (KiroUsageSnapshotRepository, bool) {
	if s == nil || s.accountRepo == nil {
		return nil, false
	}
	repo, ok := s.accountRepo.(KiroUsageSnapshotRepository)
	return repo, ok
}

// RunDue 跑一轮：所有实例都从快照恢复调度数据；只有 leader 打上游。
func (s *KiroUsageRefreshService) RunDue(ctx context.Context) error {
	repo, ok := s.snapshotRepo()
	if !ok {
		return nil
	}
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()

	candidates, err := repo.ListKiroUsageRefreshCandidates(ctx)
	if err != nil {
		return fmt.Errorf("list Kiro usage refresh candidates: %w", err)
	}
	for _, candidate := range candidates {
		if candidate.Snapshot == nil && s.Enabled() {
			// 快照被清掉（凭据变更）时，其它实例上的旧调度数据也一起作废。
			kiroPaceSnapshots.Delete(candidate.ID)
			continue
		}
		restoreKiroPaceSnapshot(candidate.ID, candidate.Snapshot)
	}
	if !s.Enabled() || len(candidates) == 0 {
		return nil
	}

	release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, kiroUsageRefreshLeaderLockKey, s.instanceID, kiroUsageRefreshLeaderLockTTL)
	if !acquired {
		return nil
	}
	defer release()

	now := s.currentTime()
	dueIDs := s.selectDue(candidates, now)
	if len(dueIDs) == 0 {
		return nil
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, dueIDs)
	if err != nil {
		return fmt.Errorf("load due Kiro accounts: %w", err)
	}

	cycleCtx, cancel := context.WithTimeout(ctx, kiroUsageRefreshCycleTimeout)
	defer cancel()
	slots := make(chan struct{}, max(int(s.concurrency.Load()), 1))
	var wg sync.WaitGroup
	for _, account := range accounts {
		if !isKiroUsageRefreshEligible(account) {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-cycleCtx.Done():
			wg.Wait()
			return nil
		}
		s.attempts.Store(account.ID, s.currentTime())
		wg.Add(1)
		go func(account *Account) {
			defer wg.Done()
			defer func() { <-slots }()
			if refreshErr := s.refreshFn(cycleCtx, account); refreshErr != nil && !errors.Is(refreshErr, ErrKiroUsageSnapshotStale) && cycleCtx.Err() == nil {
				logger.LegacyPrintf("service.kiro_usage_refresh", "refresh_failed: account_id=%d err=%v", account.ID, refreshErr)
			}
		}(account)
	}
	wg.Wait()
	return nil
}

// selectDue 按到期时间排序，返回本轮要刷新的账号 ID（最多 kiroUsageRefreshMaxPerCycle 个）。
func (s *KiroUsageRefreshService) selectDue(candidates []KiroUsageRefreshCandidate, now time.Time) []int64 {
	base := time.Duration(s.interval.Load())
	type due struct {
		id int64
		at time.Time
	}
	dues := make([]due, 0, len(candidates))
	for _, candidate := range candidates {
		if last, ok := s.attempts.Load(candidate.ID); ok && now.Sub(last.(time.Time)) < kiroUsageRefreshMinAttemptGap {
			continue
		}
		at := kiroUsageNextRefreshAt(candidate.ID, candidate.Snapshot, candidate.LastUsedAt, now, base)
		if at.After(now) {
			continue
		}
		dues = append(dues, due{id: candidate.ID, at: at})
	}
	sort.SliceStable(dues, func(i, j int) bool {
		if dues[i].at.Equal(dues[j].at) {
			return dues[i].id < dues[j].id
		}
		return dues[i].at.Before(dues[j].at)
	})
	if len(dues) > kiroUsageRefreshMaxPerCycle {
		dues = dues[:kiroUsageRefreshMaxPerCycle]
	}
	ids := make([]int64, len(dues))
	for i := range dues {
		ids[i] = dues[i].id
	}
	return ids
}

func isKiroUsageRefreshEligible(account *Account) bool {
	return account != nil && account.Platform == PlatformKiro && account.Type == AccountTypeOAuth && account.Status == StatusActive
}

// kiroUsageNextRefreshAt 计算账号的下次刷新时间；返回零值表示立即刷新。
func kiroUsageNextRefreshAt(accountID int64, snapshot *KiroUsageSnapshot, lastUsedAt *time.Time, now time.Time, base time.Duration) time.Time {
	if snapshot == nil || snapshot.LastAttemptAt.IsZero() || snapshot.refreshRequested() {
		return time.Time{}
	}
	if snapshot.FailureCount > 0 {
		return snapshot.LastAttemptAt.Add(kiroUsageRefreshBackoff(snapshot.FailureCount))
	}
	interval := kiroUsageRefreshInterval(snapshot, lastUsedAt, now, base)
	return snapshot.LastAttemptAt.Add(kiroUsageRefreshJitter(accountID, snapshot.LastAttemptAt, interval))
}

// kiroUsageRefreshInterval 返回成功后的刷新间隔：额度紧张或近期有请求时缩短。
func kiroUsageRefreshInterval(snapshot *KiroUsageSnapshot, lastUsedAt *time.Time, now time.Time, base time.Duration) time.Duration {
	if base <= 0 {
		base = kiroUsageRefreshDefaultIntervalMinutes * time.Minute
	}
	fast := min(kiroUsageRefreshFastInterval, base)
	if snapshot != nil && snapshot.KiroCredit != nil && snapshot.KiroCredit.UsageLimit > 0 &&
		snapshot.KiroCredit.PercentageUsed >= kiroUsageRefreshHotUsedPercent {
		return fast
	}
	if lastUsedAt != nil && now.Sub(*lastUsedAt) <= kiroUsageRefreshActiveWindow {
		return fast
	}
	return base
}

// kiroUsageRefreshBackoff 返回第 n 次连续失败后的等待：1、2、4…分钟，上限 1 小时。
func kiroUsageRefreshBackoff(failureCount int) time.Duration {
	if failureCount <= 0 {
		return 0
	}
	delay := kiroUsageRefreshBackoffBase
	for i := 1; i < failureCount; i++ {
		delay *= 2
		if delay >= kiroUsageRefreshBackoffMax {
			return kiroUsageRefreshBackoffMax
		}
	}
	return min(delay, kiroUsageRefreshBackoffMax)
}

// kiroUsageRefreshJitter 给间隔加 ±10% 的确定性抖动：同一账号同一次尝试每轮算出的结果一致，
// 不同账号互相错开。
func kiroUsageRefreshJitter(accountID int64, lastAttempt time.Time, interval time.Duration) time.Duration {
	spread := time.Duration(float64(interval) * kiroUsageRefreshJitterRatio)
	if spread <= 0 {
		return interval
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d:%d", accountID, lastAttempt.UnixNano())
	offset := time.Duration(h.Sum64()%uint64(2*spread+1)) - spread
	return interval + offset
}

// refreshAccount 是默认的单账号刷新：拉上游并落库。
func (s *KiroUsageRefreshService) refreshAccount(ctx context.Context, account *Account) error {
	if s.usageService == nil {
		return ErrKiroUsageRefreshUnavailable
	}
	_, _, err := s.usageService.refreshKiroUsageSnapshot(ctx, account)
	return err
}

// persistSnapshot 用专门的 repo 方法只写 extra.kiro_usage_snapshot（带 last_attempt_at CAS）。
func (s *KiroUsageRefreshService) persistSnapshot(ctx context.Context, account *Account, snapshot *KiroUsageSnapshot) error {
	repo, ok := s.snapshotRepo()
	if !ok || account == nil {
		return nil
	}
	var expected *time.Time
	if prev := decodeKiroUsageSnapshot(account.Extra); prev != nil && !prev.LastAttemptAt.IsZero() {
		at := prev.LastAttemptAt
		expected = &at
	}
	// 落库不跟随调用方取消：上游已经拉到了，别因为页面切走丢掉结果。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return repo.UpdateKiroUsageSnapshot(writeCtx, account.ID, snapshot, expected)
}

func (s *KiroUsageRefreshService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

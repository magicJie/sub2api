package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// Proxy management implementations
func (s *adminServiceImpl) ListProxies(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]Proxy, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	proxies, result, err := s.proxyRepo.ListWithFilters(ctx, params, protocol, status, search)
	if err != nil {
		return nil, 0, err
	}
	return proxies, result.Total, nil
}

func (s *adminServiceImpl) ListProxiesWithAccountCount(ctx context.Context, page, pageSize int, protocol, status, search string, sortBy, sortOrder string) ([]ProxyWithAccountCount, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	proxies, result, err := s.proxyRepo.ListWithFiltersAndAccountCount(ctx, params, protocol, status, search)
	if err != nil {
		return nil, 0, err
	}
	s.attachProxyLatency(ctx, proxies)
	return proxies, result.Total, nil
}

func (s *adminServiceImpl) GetAllProxies(ctx context.Context) ([]Proxy, error) {
	return s.proxyRepo.ListActive(ctx)
}

func (s *adminServiceImpl) GetAllProxiesWithAccountCount(ctx context.Context) ([]ProxyWithAccountCount, error) {
	proxies, err := s.proxyRepo.ListActiveWithAccountCount(ctx)
	if err != nil {
		return nil, err
	}
	s.attachProxyLatency(ctx, proxies)
	return proxies, nil
}

func (s *adminServiceImpl) GetProxy(ctx context.Context, id int64) (*Proxy, error) {
	return s.proxyRepo.GetByID(ctx, id)
}

func (s *adminServiceImpl) GetProxiesByIDs(ctx context.Context, ids []int64) ([]Proxy, error) {
	return s.proxyRepo.ListByIDs(ctx, ids)
}

func (s *adminServiceImpl) CreateProxy(ctx context.Context, input *CreateProxyInput) (*Proxy, error) {
	if !isJSONTimeInRange(input.ExpiresAt) {
		return nil, infraerrors.BadRequest("PROXY_EXPIRY_INVALID", "proxy expiry year must be between 0 and 9999")
	}
	// 规范化 fallback_mode
	mode := input.FallbackMode
	if mode == "" {
		mode = FallbackModeNone
	}
	// 校验：mode=proxy 必须有 backup
	if mode == FallbackModeProxy && input.BackupProxyID == nil {
		return nil, infraerrors.BadRequest("PROXY_BACKUP_REQUIRED", "backup proxy required when fallback_mode=proxy")
	}
	if input.ExpiryWarnDays < 0 {
		return nil, infraerrors.BadRequest("PROXY_WARN_DAYS_INVALID", "expiry_warn_days must be >= 0")
	}

	proxy := &Proxy{
		Name:           input.Name,
		Protocol:       input.Protocol,
		Host:           input.Host,
		Port:           input.Port,
		Username:       input.Username,
		Password:       input.Password,
		Status:         StatusActive,
		ExpiresAt:      input.ExpiresAt,
		FallbackMode:   mode,
		BackupProxyID:  input.BackupProxyID,
		ExpiryWarnDays: input.ExpiryWarnDays,
	}
	poolID, err := normalizeProxyPoolRef(ctx, input.PoolID) // 代理池（fork）
	if err != nil {
		return nil, err
	}
	proxy.PoolID = poolID
	if err := checkProxyEgressHost(ctx, proxy, nil); err != nil { // 代理池（fork）
		return nil, err
	}
	if err := applyProxyChainCreate(ctx, proxy, &input.ProxyChainInput); err != nil { // 链式代理（fork）
		return nil, err
	}
	if err := s.proxyRepo.Create(ctx, proxy); err != nil {
		return nil, err
	}
	RefreshProxyChainSnapshot(ctx) // 链式代理（fork）
	// Probe latency asynchronously so creation isn't blocked by network timeout.
	go s.probeProxyLatency(context.Background(), proxy)
	return proxy, nil
}

func (s *adminServiceImpl) UpdateProxy(ctx context.Context, id int64, input *UpdateProxyInput) (*Proxy, error) {
	if !isJSONTimeInRange(input.ExpiresAt) {
		return nil, infraerrors.BadRequest("PROXY_EXPIRY_INVALID", "proxy expiry year must be between 0 and 9999")
	}
	// 校验：backup_proxy_id 不能是自身
	if input.BackupProxyID != nil && *input.BackupProxyID == id {
		return nil, infraerrors.BadRequest("PROXY_BACKUP_SELF", "backup proxy cannot be itself")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	prev := *proxy // 代理池（fork）：角色约束需要编辑前的状态

	// Merge only supplied fields, then validate the resulting fallback configuration.
	mode := proxy.FallbackMode
	if input.FallbackMode != "" {
		mode = input.FallbackMode
	}
	backupID := proxy.BackupProxyID
	if input.BackupProxyID != nil || input.ClearBackupID {
		backupID = input.BackupProxyID
	}
	if mode == FallbackModeProxy && backupID == nil {
		return nil, infraerrors.BadRequest("PROXY_BACKUP_REQUIRED", "backup proxy required when fallback_mode=proxy")
	}
	if input.ExpiryWarnDays != nil && *input.ExpiryWarnDays < 0 {
		return nil, infraerrors.BadRequest("PROXY_WARN_DAYS_INVALID", "expiry_warn_days must be >= 0")
	}

	if input.Name != "" {
		proxy.Name = input.Name
	}
	if input.Protocol != "" {
		proxy.Protocol = input.Protocol
	}
	if input.Host != "" {
		proxy.Host = input.Host
	}
	if input.Port != 0 {
		proxy.Port = input.Port
	}
	if input.Username != nil {
		proxy.Username = *input.Username
	}
	if input.Password != nil {
		proxy.Password = *input.Password
	}
	if input.Status != "" {
		proxy.Status = input.Status
	}
	if input.ExpiresAt != nil || input.ClearExpiresAt {
		proxy.ExpiresAt = input.ExpiresAt
	}
	proxy.FallbackMode = mode
	proxy.BackupProxyID = backupID
	if input.ExpiryWarnDays != nil {
		proxy.ExpiryWarnDays = *input.ExpiryWarnDays
	}
	if input.PoolIDSet { // 代理池（fork）
		if proxy.PoolID, err = normalizeProxyPoolRef(ctx, input.PoolID); err != nil {
			return nil, err
		}
	}
	if err := checkProxyEgressHost(ctx, proxy, &prev); err != nil { // 代理池（fork）
		return nil, err
	}
	if err := applyProxyChainUpdate(ctx, proxy, &input.ProxyChainUpdateInput); err != nil { // 链式代理（fork）
		return nil, err
	}

	if err := s.proxyRepo.Update(ctx, proxy); err != nil {
		return nil, err
	}
	RefreshProxyChainSnapshot(ctx) // 链式代理（fork）
	return proxy, nil
}

func (s *adminServiceImpl) DeleteProxy(ctx context.Context, id int64) error {
	count, err := s.proxyRepo.CountAccountsByProxyID(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrProxyInUse
	}
	if err := s.proxyRepo.Delete(ctx, id); err != nil {
		return err
	}
	RefreshProxyChainSnapshot(ctx) // 链式代理（fork）
	return nil
}

func (s *adminServiceImpl) BatchDeleteProxies(ctx context.Context, ids []int64) (*ProxyBatchDeleteResult, error) {
	result := &ProxyBatchDeleteResult{}
	if len(ids) == 0 {
		return result, nil
	}

	for _, id := range ids {
		count, err := s.proxyRepo.CountAccountsByProxyID(ctx, id)
		if err != nil {
			result.Skipped = append(result.Skipped, ProxyBatchDeleteSkipped{
				ID:     id,
				Reason: err.Error(),
			})
			continue
		}
		if count > 0 {
			result.Skipped = append(result.Skipped, ProxyBatchDeleteSkipped{
				ID:     id,
				Reason: ErrProxyInUse.Error(),
			})
			continue
		}
		if err := s.proxyRepo.Delete(ctx, id); err != nil {
			result.Skipped = append(result.Skipped, ProxyBatchDeleteSkipped{
				ID:     id,
				Reason: err.Error(),
			})
			continue
		}
		result.DeletedIDs = append(result.DeletedIDs, id)
	}
	RefreshProxyChainSnapshot(ctx) // 链式代理（fork）

	return result, nil
}

func (s *adminServiceImpl) GetProxyAccounts(ctx context.Context, proxyID int64) ([]ProxyAccountSummary, error) {
	return s.proxyRepo.ListAccountSummariesByProxyID(ctx, proxyID)
}

func (s *adminServiceImpl) CheckProxyExists(ctx context.Context, host string, port int, username, password string) (bool, error) {
	return s.proxyRepo.ExistsByHostPortAuth(ctx, host, port, username, password)
}

// probeProxyChain 探测代理出口。前置链路解析失败（如前置组没有可用槽位）时直接返回原因，
// 不去拨 fail-closed 的占位地址（否则报错只有 unsupported proxy scheme）。
func (s *adminServiceImpl) probeProxyChain(ctx context.Context, proxy *Proxy) (*ProxyExitInfo, int64, error) {
	if _, chainErr := DescribeProxyChain(proxy); chainErr != "" {
		return nil, 0, errors.New(chainErr)
	}
	return s.proxyProber.ProbeProxy(ctx, proxy.URL())
}

func (s *adminServiceImpl) TestProxy(ctx context.Context, id int64) (*ProxyTestResult, error) {
	proxy, err := s.proxyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	exitInfo, latencyMs, err := s.probeProxyChain(ctx, proxy)
	if err != nil {
		s.saveProxyLatency(ctx, id, &ProxyLatencyInfo{
			Success:   false,
			Message:   err.Error(),
			UpdatedAt: time.Now(),
		})
		return &ProxyTestResult{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	latency := latencyMs
	s.saveProxyLatency(ctx, id, &ProxyLatencyInfo{
		Success:     true,
		LatencyMs:   &latency,
		Message:     "Proxy is accessible",
		IPAddress:   exitInfo.IP,
		Country:     exitInfo.Country,
		CountryCode: exitInfo.CountryCode,
		Region:      exitInfo.Region,
		City:        exitInfo.City,
		UpdatedAt:   time.Now(),
	})
	return &ProxyTestResult{
		Success:     true,
		Message:     "Proxy is accessible",
		LatencyMs:   latencyMs,
		IPAddress:   exitInfo.IP,
		City:        exitInfo.City,
		Region:      exitInfo.Region,
		Country:     exitInfo.Country,
		CountryCode: exitInfo.CountryCode,
	}, nil
}

func (s *adminServiceImpl) CheckProxyQuality(ctx context.Context, id int64) (*ProxyQualityCheckResult, error) {
	proxy, err := s.proxyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	result := &ProxyQualityCheckResult{
		ProxyID:   id,
		Score:     100,
		Grade:     "A",
		CheckedAt: time.Now().Unix(),
		Items:     make([]ProxyQualityCheckItem, 0, len(proxyQualityTargets)+3),
	}

	proxyURL := proxy.URL()
	if s.proxyProber == nil {
		result.Items = append(result.Items, ProxyQualityCheckItem{
			Target:  "base_connectivity",
			Status:  "fail",
			Message: "代理探测服务未配置",
		})
		result.FailedCount++
		finalizeProxyQualityResult(result)
		s.saveProxyQualitySnapshot(ctx, proxy, result, nil)
		return result, nil
	}

	exitInfo, latencyMs, err := s.probeProxyChain(ctx, proxy)
	if err != nil {
		result.Items = append(result.Items, ProxyQualityCheckItem{
			Target:    "base_connectivity",
			Status:    "fail",
			LatencyMs: latencyMs,
			Message:   err.Error(),
		})
		result.FailedCount++
		finalizeProxyQualityResult(result)
		s.saveProxyQualitySnapshot(ctx, proxy, result, nil)
		return result, nil
	}

	result.ExitIP = exitInfo.IP
	result.Country = exitInfo.Country
	result.CountryCode = exitInfo.CountryCode
	result.BaseLatencyMs = latencyMs
	result.Items = append(result.Items, ProxyQualityCheckItem{
		Target:    "base_connectivity",
		Status:    "pass",
		LatencyMs: latencyMs,
		Message:   "代理出口连通正常",
	})
	result.PassedCount++

	if host, err := netip.ParseAddr(strings.TrimSpace(proxy.Host)); err != nil {
		result.Items = append(result.Items, ProxyQualityCheckItem{Target: "exit_identity", Status: "fail", Message: "静态落地代理 host 必须是出口 IP，域名无法确认固定出口"})
		result.FailedCount++
	} else if exit, err := netip.ParseAddr(exitInfo.IP); err != nil || host.Unmap() != exit.Unmap() {
		result.Items = append(result.Items, ProxyQualityCheckItem{Target: "exit_identity", Status: "fail", Message: "实际出口与登记落地 IP 不一致"})
		result.FailedCount++
	}

	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL:              proxyURL,
		Timeout:               proxyQualityRequestTimeout,
		ResponseHeaderTimeout: proxyQualityResponseHeaderTimeout,
	})
	if err != nil {
		result.Items = append(result.Items, ProxyQualityCheckItem{
			Target:  "http_client",
			Status:  "fail",
			Message: fmt.Sprintf("创建检测客户端失败: %v", err),
		})
		result.FailedCount++
		finalizeProxyQualityResult(result)
		s.saveProxyQualitySnapshot(ctx, proxy, result, exitInfo)
		return result, nil
	}
	items := make([]ProxyQualityCheckItem, len(proxyQualityTargets))
	var checks sync.WaitGroup
	checks.Add(len(proxyQualityTargets) + 1)
	go func() {
		defer checks.Done()
		result.Reputation = fetchProxyIPReputation(ctx, client, "https://ipok.io/api/ip", exitInfo.IP, time.Now())
	}()
	for i, target := range proxyQualityTargets {
		go func() {
			defer checks.Done()
			items[i] = runProxyQualityTarget(ctx, client, target)
		}()
	}
	checks.Wait()
	reputationItem := ProxyQualityCheckItem{Target: "ip_reputation", Status: "pass", Message: result.Reputation.Reason}
	if result.Reputation.RiskScore == nil {
		reputationItem.Status = "warn"
		result.WarnCount++
	} else if *result.Reputation.RiskScore >= 50 {
		reputationItem.Status = "fail"
		reputationItem.Message = fmt.Sprintf("IP 风险 %d/100，达到高风险参考阈值 50，建议优先选择低风险 IP；信号：%s", *result.Reputation.RiskScore, strings.Join(result.Reputation.Signals, ", "))
		result.FailedCount++
	} else {
		reputationItem.Message = fmt.Sprintf("IP 风险 %d/100；%s", *result.Reputation.RiskScore, result.Reputation.Reason)
		result.PassedCount++
	}
	result.Items = append(result.Items, reputationItem)

	for _, item := range items {
		result.Items = append(result.Items, item)
		switch item.Status {
		case "pass":
			result.PassedCount++
		case "warn":
			result.WarnCount++
		case "challenge":
			result.ChallengeCount++
		default:
			result.FailedCount++
		}
	}

	finalizeProxyQualityResult(result)
	s.saveProxyQualitySnapshot(ctx, proxy, result, exitInfo)
	return result, nil
}

func runProxyQualityTarget(ctx context.Context, client *http.Client, target proxyQualityTarget) ProxyQualityCheckItem {
	item := ProxyQualityCheckItem{Target: target.Target, Platform: target.Platform, Surface: target.Surface, URL: target.URL, ObservedAt: time.Now().Unix()}
	var requestBody io.Reader
	if target.Body != "" {
		requestBody = strings.NewReader(target.Body)
	}
	req, err := http.NewRequestWithContext(ctx, target.Method, target.URL, requestBody)
	if err != nil {
		item.Status, item.Evidence, item.Message = "warn", "invalid_response", "无法构建 AI 检测请求"
		return item
	}
	req.Header.Set("Accept", "application/json,text/html,*/*")
	req.Header.Set("User-Agent", proxyQualityClientUserAgent)
	if target.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if target.Target == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	start := time.Now()
	resp, err := client.Do(req)
	item.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		item.Status, item.Evidence, item.Message = "warn", "network_error", "AI 服务请求失败或超时；本次可达性未确认，需复检"
		return item
	}
	defer resp.Body.Close()
	item.HTTPStatus = resp.StatusCode
	item.FinalURL = resp.Request.URL.String()
	item.RetryAfter = resp.Header.Get("Retry-After")
	maxBody := proxyQualityMaxBodyBytes
	if target.Surface == "web" {
		maxBody = 64 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		item.Status, item.Evidence, item.Message = "warn", "network_error", "AI 服务响应读取失败，需复检"
		return item
	}
	if int64(len(body)) > maxBody {
		body = body[:maxBody]
	}
	classifyProxyAIResponse(&item, target, resp, body)
	return item
}

func finalizeProxyQualityResult(result *ProxyQualityCheckResult) {
	if result == nil {
		return
	}
	score := max(0, 100-result.WarnCount*10-result.FailedCount*22-result.ChallengeCount*30)
	result.Eligible, result.BaseEligible = false, false
	switch {
	case result.Reputation == nil || result.Reputation.Status != "current" || result.Reputation.RiskScore == nil:
		score = 0
		result.AdmissionReason = "IP 风险证据不足，暂不推荐优先分配"
		if result.Reputation != nil {
			result.AdmissionReason = result.Reputation.Reason
		}
	case *result.Reputation.RiskScore >= 50:
		score = min(score, 100-*result.Reputation.RiskScore)
		result.AdmissionReason = "IP 风险达到高风险参考阈值 50，建议优先选择低风险 IP"
	default:
		score = min(score, 100-*result.Reputation.RiskScore)
		result.BaseEligible = proxyQualityBaseConnectivityPass(result)
		for _, item := range result.Items {
			if item.Platform == "" && item.Target != "ip_reputation" && item.Status != "pass" {
				result.BaseEligible = false
			}
		}
		result.AdmissionReason = "基础出口或出口身份校验未通过，暂不推荐优先分配"
		if result.BaseEligible {
			result.AdmissionReason = "基础 IP 检查通过；按账号实际使用的 AI 路径分别给出推荐"
		}
	}
	assessProxyAIPlatforms(result)
	if result.BaseEligible && !result.Eligible {
		result.AdmissionReason = "基础 IP 检查通过，AI 路径尚无推荐；可查看各服务证据并复检"
	}
	result.Score = score
	result.Grade = proxyQualityGrade(score)
	if result.Reputation == nil || result.Reputation.RiskScore == nil {
		result.Grade = "Unknown"
	}
	result.Summary = fmt.Sprintf("通过 %d 项，告警 %d 项，失败 %d 项，挑战 %d 项；%s",
		result.PassedCount, result.WarnCount, result.FailedCount, result.ChallengeCount, result.AdmissionReason)
}

func proxyQualityGrade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 75:
		return "B"
	case score >= 60:
		return "C"
	case score >= 40:
		return "D"
	default:
		return "F"
	}
}

func proxyQualityOverallStatus(result *ProxyQualityCheckResult) string {
	if result == nil {
		return ""
	}
	if result.BaseEligible && result.Eligible {
		if result.ChallengeCount+result.FailedCount+result.WarnCount > 0 {
			return "warn"
		}
		return "healthy"
	}
	if result.ChallengeCount > 0 {
		return "challenge"
	}
	if result.FailedCount > 0 {
		return "failed"
	}
	if result.WarnCount > 0 {
		return "warn"
	}
	if !result.Eligible {
		return "warn"
	}
	if result.PassedCount > 0 {
		return "healthy"
	}
	return "failed"
}

func proxyQualityFirstCFRay(result *ProxyQualityCheckResult) string {
	if result == nil {
		return ""
	}
	for _, item := range result.Items {
		if item.CFRay != "" {
			return item.CFRay
		}
	}
	return ""
}

func proxyQualityBaseConnectivityPass(result *ProxyQualityCheckResult) bool {
	if result == nil {
		return false
	}
	for _, item := range result.Items {
		if item.Target == "base_connectivity" {
			return item.Status == "pass"
		}
	}
	return false
}

func (s *adminServiceImpl) saveProxyQualitySnapshot(ctx context.Context, proxy *Proxy, result *ProxyQualityCheckResult, exitInfo *ProxyExitInfo) {
	if result == nil {
		return
	}
	score := result.Score
	checkedAt := result.CheckedAt
	info := &ProxyLatencyInfo{
		Success:             proxyQualityBaseConnectivityPass(result),
		Message:             result.Summary,
		QualityStatus:       proxyQualityOverallStatus(result),
		QualityScore:        &score,
		QualityGrade:        result.Grade,
		QualitySummary:      result.Summary,
		QualityCheckedAt:    &checkedAt,
		QualityCFRay:        proxyQualityFirstCFRay(result),
		QualityBaseEligible: result.BaseEligible,
		QualityAIPlatforms:  result.AIPlatforms,
		QualityFingerprint:  proxyQualityFingerprint(proxy),
		UpdatedAt:           time.Now(),
	}
	if result.Reputation != nil {
		info.QualityValidUntil = result.Reputation.ValidUntil
	}
	if result.BaseLatencyMs > 0 {
		latency := result.BaseLatencyMs
		info.LatencyMs = &latency
	}
	if exitInfo != nil {
		info.IPAddress = exitInfo.IP
		info.Country = exitInfo.Country
		info.CountryCode = exitInfo.CountryCode
		info.Region = exitInfo.Region
		info.City = exitInfo.City
	}
	s.saveProxyLatency(ctx, proxy.ID, info)
}

func (s *adminServiceImpl) probeProxyLatency(ctx context.Context, proxy *Proxy) {
	if s.proxyProber == nil || proxy == nil {
		return
	}
	exitInfo, latencyMs, err := s.proxyProber.ProbeProxy(ctx, proxy.URL())
	if err != nil {
		s.saveProxyLatency(ctx, proxy.ID, &ProxyLatencyInfo{
			Success:   false,
			Message:   err.Error(),
			UpdatedAt: time.Now(),
		})
		return
	}

	latency := latencyMs
	s.saveProxyLatency(ctx, proxy.ID, &ProxyLatencyInfo{
		Success:     true,
		LatencyMs:   &latency,
		Message:     "Proxy is accessible",
		IPAddress:   exitInfo.IP,
		Country:     exitInfo.Country,
		CountryCode: exitInfo.CountryCode,
		Region:      exitInfo.Region,
		City:        exitInfo.City,
		UpdatedAt:   time.Now(),
	})
}

func (s *adminServiceImpl) attachProxyLatency(ctx context.Context, proxies []ProxyWithAccountCount) {
	if s.proxyLatencyCache == nil || len(proxies) == 0 {
		return
	}

	ids := make([]int64, 0, len(proxies))
	for i := range proxies {
		ids = append(ids, proxies[i].ID)
	}

	latencies, err := s.proxyLatencyCache.GetProxyLatencies(ctx, ids)
	if err != nil {
		logger.LegacyPrintf("service.admin", "Warning: load proxy latency cache failed: %v", err)
		return
	}

	for i := range proxies {
		info := latencies[proxies[i].ID]
		if info == nil {
			continue
		}
		if info.Success {
			proxies[i].LatencyStatus = "success"
			proxies[i].LatencyMs = info.LatencyMs
		} else {
			proxies[i].LatencyStatus = "failed"
		}
		proxies[i].LatencyMessage = info.Message
		proxies[i].IPAddress = info.IPAddress
		proxies[i].Country = info.Country
		proxies[i].CountryCode = info.CountryCode
		proxies[i].Region = info.Region
		proxies[i].City = info.City
		proxies[i].QualityStatus = info.QualityStatus
		proxies[i].QualityScore = info.QualityScore
		proxies[i].QualityGrade = info.QualityGrade
		proxies[i].QualitySummary = info.QualitySummary
		proxies[i].QualityChecked = info.QualityCheckedAt
		if info.QualityStatus == "healthy" && !proxyQualityRecommended(&proxies[i].Proxy, info, time.Now()) {
			proxies[i].QualityStatus = "warn"
			proxies[i].QualityScore = nil
			proxies[i].QualityGrade = "Unknown"
			proxies[i].QualitySummary = "质量证据已失效或代理配置已变化，暂不推荐优先分配；可重新检测更新推荐"
		}
	}
}

func (s *adminServiceImpl) saveProxyLatency(ctx context.Context, proxyID int64, info *ProxyLatencyInfo) {
	if s.proxyLatencyCache == nil || info == nil {
		return
	}

	merged := *info
	if latencies, err := s.proxyLatencyCache.GetProxyLatencies(ctx, []int64{proxyID}); err == nil {
		if existing := latencies[proxyID]; existing != nil {
			if merged.QualityCheckedAt == nil &&
				merged.QualityScore == nil &&
				merged.QualityGrade == "" &&
				merged.QualityStatus == "" &&
				merged.QualitySummary == "" &&
				merged.QualityCFRay == "" {
				merged.QualityStatus = existing.QualityStatus
				merged.QualityScore = existing.QualityScore
				merged.QualityGrade = existing.QualityGrade
				merged.QualitySummary = existing.QualitySummary
				merged.QualityCheckedAt = existing.QualityCheckedAt
				merged.QualityCFRay = existing.QualityCFRay
				merged.QualityBaseEligible = existing.QualityBaseEligible
				merged.QualityAIPlatforms = existing.QualityAIPlatforms
				merged.QualityFingerprint = existing.QualityFingerprint
				merged.QualityValidUntil = existing.QualityValidUntil
			}
		}
	}

	if err := s.proxyLatencyCache.SetProxyLatency(ctx, proxyID, &merged); err != nil {
		logger.LegacyPrintf("service.admin", "Warning: store proxy latency cache failed: %v", err)
	}
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const proxyQualityMaxAge = 24 * time.Hour

// ProxyIPReputation describes current IP-layer evidence, separate from reachability.
type ProxyIPReputation struct {
	Status     string                    `json:"status"`
	RiskScore  *int                      `json:"risk_score"`
	Signals    []string                  `json:"signals"`
	Sources    []ProxyIPReputationSource `json:"sources"`
	CheckedAt  int64                     `json:"checked_at"`
	ValidUntil int64                     `json:"valid_until"`
	Reason     string                    `json:"reason"`
}

type ProxyIPReputationSource struct {
	Source     string `json:"source"`
	RiskScore  *int   `json:"risk_score,omitempty"`
	ObservedAt string `json:"observed_at"`
}

// Only decode the public evidence contract; raw provider payloads are not retained.
type ipokReputationResponse struct {
	Geo struct {
		IP string `json:"ip"`
	} `json:"geo"`
	Risk           *int `json:"risk"`
	RiskAssessment *struct {
		Version        int      `json:"version"`
		Status         string   `json:"status"`
		ScoreAvailable bool     `json:"scoreAvailable"`
		CurrentSources []string `json:"currentSources"`
		StaleSources   []string `json:"staleSources"`
		UndatedSources []string `json:"undatedSources"`
		InvalidSources []string `json:"invalidSources"`
		AssessedAt     string   `json:"assessedAt"`
	} `json:"riskAssessment"`
	RiskBreakdown struct {
		Floors []struct {
			Key   string `json:"key"`
			Floor int    `json:"floor"`
		} `json:"floors"`
	} `json:"riskBreakdown"`
	Signals []string `json:"signals"`
	Sources []struct {
		Source     string `json:"source"`
		OK         bool   `json:"ok"`
		Risk       *int   `json:"risk"`
		ObservedAt string `json:"observedAt"`
	} `json:"sources"`
	Blocklist struct {
		Listed  []string `json:"listed"`
		Checked int      `json:"checked"`
		Total   int      `json:"total"`
	} `json:"blocklist"`
}

func unknownProxyReputation(now time.Time, reason string) *ProxyIPReputation {
	return &ProxyIPReputation{Status: "insufficient", Signals: []string{}, Sources: []ProxyIPReputationSource{}, CheckedAt: now.Unix(), Reason: reason}
}

func fetchProxyIPReputation(ctx context.Context, client *http.Client, endpoint, ip string, now time.Time) *ProxyIPReputation {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return unknownProxyReputation(now, "出口不是有效公网 IP")
	}
	// Self-lookup uses the tested proxy's source IP and its own quota bucket.
	// The returned identity is checked against the independently probed exit below.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unknownProxyReputation(now, "无法构建 IP 信誉检测请求")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Version", "1")
	req.Header.Set("User-Agent", "sub2api-ip-quality/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return unknownProxyReputation(now, "IPOK 请求失败或超时，证据不足")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return unknownProxyReputation(now, fmt.Sprintf("IPOK 返回 HTTP %d（可能额度不足），证据不足", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
	if err != nil || len(body) > 512*1024 {
		return unknownProxyReputation(now, "IPOK 响应读取失败或超出大小限制")
	}
	var data ipokReputationResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return unknownProxyReputation(now, "IPOK 响应格式无效，证据不足")
	}
	return assessProxyIPReputation(&data, addr.Unmap(), now)
}

func assessProxyIPReputation(data *ipokReputationResponse, addr netip.Addr, now time.Time) *ProxyIPReputation {
	result := unknownProxyReputation(now, "IPOK 缺少当前多源风险证据")
	returnedIP, err := netip.ParseAddr(data.Geo.IP)
	if err != nil || returnedIP.Unmap() != addr {
		result.Reason = "信誉结果与探测出口 IP 不一致"
		return result
	}
	seen := make(map[string]bool, len(data.Sources))
	for _, source := range data.Sources {
		if !source.OK || seen[source.Source] {
			continue
		}
		seen[source.Source] = true
		result.Sources = append(result.Sources, ProxyIPReputationSource{Source: source.Source, RiskScore: source.Risk, ObservedAt: source.ObservedAt})
	}
	result.Signals = append(result.Signals, data.Signals...)
	assessment := data.RiskAssessment
	if assessment == nil || assessment.Version != 1 || !assessment.ScoreAvailable ||
		(assessment.Status != "current" && assessment.Status != "limited") ||
		len(assessment.StaleSources)+len(assessment.UndatedSources)+len(assessment.InvalidSources) != 0 {
		return result
	}
	assessedAt, err := time.Parse(time.RFC3339Nano, assessment.AssessedAt)
	if err != nil || assessedAt.After(now.Add(time.Minute)) || now.Sub(assessedAt) > proxyQualityMaxAge {
		result.Reason = "IPOK 评估时间缺失或过期"
		return result
	}
	validUntil := now.Add(proxyQualityMaxAge)
	if expires := assessedAt.Add(proxyQualityMaxAge); expires.Before(validUntil) {
		validUntil = expires
	}
	current := make(map[string]bool, len(assessment.CurrentSources))
	for _, source := range assessment.CurrentSources {
		current[source] = true
	}
	// Recommendations need both anonymity and abuse coverage, not just geolocation.
	// Vendor names do not establish independence of their underlying databases.
	anonymousCoverage, abuseCoverage := false, false
	for _, source := range result.Sources {
		if source.RiskScore == nil || source.Source == "IPinfo Lite" || source.Source == "IPOK-DB" {
			continue
		}
		if !current[source.Source] {
			result.Reason = "数值风险来源缺少当前有效证据"
			return result
		}
		observedAt, err := time.Parse(time.RFC3339Nano, source.ObservedAt)
		if err != nil || observedAt.After(now.Add(time.Minute)) || now.Sub(observedAt) >= 72*time.Hour || *source.RiskScore < 0 || *source.RiskScore > 100 {
			result.Reason = "风险来源时间或评分无效，证据不足"
			return result
		}
		if expires := observedAt.Add(72 * time.Hour); expires.Before(validUntil) {
			validUntil = expires
		}
		switch source.Source {
		case "Scamalytics", "proxycheck", "IPHub", "vpnapi.io", "IPQS":
			anonymousCoverage = true
		case "AbuseIPDB", "ipapi.is", "StopForumSpam":
			abuseCoverage = true
		}
	}
	if !anonymousCoverage || !abuseCoverage || data.Risk == nil || *data.Risk < 0 || *data.Risk > 100 {
		return result
	}
	// A failed blacklist check is unknown, never a clean result. IPv6 has no DNSBL coverage here.
	if addr.Is4() && (data.Blocklist.Checked <= 0 || data.Blocklist.Total <= 0 || data.Blocklist.Checked != data.Blocklist.Total) {
		result.Reason = "DNSBL 黑名单检测未完整完成，证据不足"
		return result
	}
	risk := *data.Risk
	signals := make(map[string]bool)
	for _, signal := range data.Signals {
		signals[signal] = true
	}
	for _, floor := range data.RiskBreakdown.Floors {
		if floor.Floor < 0 || floor.Floor > 100 {
			return result
		}
		if floor.Floor > risk {
			risk = floor.Floor
		}
		signals[floor.Key] = true
	}
	if len(data.Blocklist.Listed) > 0 {
		signals["blacklist"] = true
	}
	// Direct evidence must survive averaging, even if an upstream aggregate is low.
	result.Signals = result.Signals[:0]
	for signal := range signals {
		floor := 0
		switch signal {
		case "tor":
			floor = 90
		case "blacklist", "blocklisted":
			floor = 70
		case "proxy", "vpn":
			floor = 65
		case "abuser", "abuse":
			floor = 55
		}
		if risk < floor {
			risk = floor
		}
		result.Signals = append(result.Signals, signal)
	}
	sort.Strings(result.Signals)
	result.Status, result.RiskScore, result.ValidUntil = "current", &risk, validUntil.Unix()
	result.Reason = "当前多源 IP 风险证据可用"
	return result
}

// Hash stored configuration rather than URL(), which can contain an ephemeral chain listener.
func proxyQualityFingerprint(proxy *Proxy) string {
	config := struct {
		Protocol, Host, Username, Password, UpstreamMode string
		Port                                             int
		UpstreamGroupID                                  *int64
		Options                                          map[string]string
	}{proxy.Protocol, strings.TrimSpace(proxy.Host), proxy.Username, proxy.Password, NormalizeUpstreamMode(proxy.UpstreamMode), proxy.Port, proxy.UpstreamGroupID, proxy.Options}
	body, _ := json.Marshal(config)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func proxyQualityRecommended(proxy *Proxy, info *ProxyLatencyInfo, now time.Time) bool {
	if info == nil || !info.Success || !info.QualityBaseEligible ||
		info.QualityCheckedAt == nil || *info.QualityCheckedAt > now.Unix() ||
		now.Sub(time.Unix(*info.QualityCheckedAt, 0)) >= proxyQualityMaxAge ||
		info.QualityValidUntil <= now.Unix() || info.QualityFingerprint != proxyQualityFingerprint(proxy) {
		return false
	}
	host, err := netip.ParseAddr(strings.TrimSpace(proxy.Host))
	if err != nil {
		return false
	}
	exit, err := netip.ParseAddr(info.IPAddress)
	return err == nil && host.Unmap() == exit.Unmap()
}

func proxyQualityRecommendedForTarget(proxy *Proxy, info *ProxyLatencyInfo, target string, now time.Time) bool {
	if !proxyQualityRecommended(proxy, info, now) {
		return false
	}
	if target == "" {
		return true
	}
	for _, assessment := range info.QualityAIPlatforms {
		if assessment.Target == target {
			return assessment.Eligible && assessment.Status == "reachable"
		}
	}
	return false
}

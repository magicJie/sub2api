package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/stretchr/testify/require"
)

func reputationFixture(now time.Time) *ipokReputationResponse {
	body := fmt.Sprintf(`{"geo":{"ip":"8.8.8.8"},"risk":0,
	"riskAssessment":{"version":1,"status":"limited","scoreAvailable":true,"currentSources":["Scamalytics","AbuseIPDB"],"assessedAt":%q},
	"sources":[{"source":"Scamalytics","ok":true,"risk":0,"observedAt":%q},{"source":"AbuseIPDB","ok":true,"risk":0,"observedAt":%q}],
	"blocklist":{"listed":[],"checked":5,"total":5}}`, now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339))
	var data ipokReputationResponse
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		panic(err)
	}
	return &data
}

func TestProxyReputationHardSignalsSurviveLowAverage(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for signal, floor := range map[string]int{"tor": 90, "proxy": 65, "vpn": 65, "abuser": 55, "blacklist": 70, "blocklisted": 70} {
		t.Run(signal, func(t *testing.T) {
			data := reputationFixture(now)
			data.Signals = []string{signal}
			reputation := assessProxyIPReputation(data, netip.MustParseAddr("8.8.8.8"), now)
			require.Equal(t, floor, *reputation.RiskScore)
			result := &ProxyQualityCheckResult{PassedCount: 5, Items: []ProxyQualityCheckItem{{Target: "base_connectivity", Status: "pass"}}, Reputation: reputation}
			finalizeProxyQualityResult(result)
			require.False(t, result.Eligible)
			require.LessOrEqual(t, result.Score, 100-floor)
			require.NotEqual(t, "healthy", proxyQualityOverallStatus(result))
		})
	}
}

func TestProxyReputationRejectsInsufficientEvidence(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(*ipokReputationResponse)
	}{
		{"missing assessment", func(d *ipokReputationResponse) { d.RiskAssessment = nil }},
		{"score unavailable", func(d *ipokReputationResponse) { d.RiskAssessment.ScoreAvailable = false }},
		{"missing score", func(d *ipokReputationResponse) { d.Risk = nil }},
		{"stale assessment", func(d *ipokReputationResponse) {
			d.RiskAssessment.AssessedAt = now.Add(-25 * time.Hour).Format(time.RFC3339)
		}},
		{"stale contribution", func(d *ipokReputationResponse) {
			d.Sources[0].ObservedAt = now.Add(-72 * time.Hour).Format(time.RFC3339)
		}},
		{"undated contribution", func(d *ipokReputationResponse) { d.Sources[0].ObservedAt = "" }},
		{"future contribution", func(d *ipokReputationResponse) { d.Sources[0].ObservedAt = now.Add(time.Hour).Format(time.RFC3339) }},
		{"one source", func(d *ipokReputationResponse) { d.Sources = d.Sources[:1] }},
		{"duplicate sources", func(d *ipokReputationResponse) { d.Sources[1] = d.Sources[0] }},
		{"missing anonymity coverage", func(d *ipokReputationResponse) {
			d.Sources[0].Source = "ip-api"
			d.RiskAssessment.CurrentSources[0] = "ip-api"
		}},
		{"missing abuse coverage", func(d *ipokReputationResponse) {
			d.Sources[1].Source = "ip-api"
			d.RiskAssessment.CurrentSources[1] = "ip-api"
		}},
		{"partial blacklist", func(d *ipokReputationResponse) { d.Blocklist.Checked = 4 }},
		{"all blacklist failed", func(d *ipokReputationResponse) { d.Blocklist.Checked = 0 }},
		{"wrong IP", func(d *ipokReputationResponse) { d.Geo.IP = "1.1.1.1" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := reputationFixture(now)
			tc.mutate(data)
			reputation := assessProxyIPReputation(data, netip.MustParseAddr("8.8.8.8"), now)
			require.Equal(t, "insufficient", reputation.Status)
			require.Nil(t, reputation.RiskScore)
			result := &ProxyQualityCheckResult{PassedCount: 5, Reputation: reputation}
			finalizeProxyQualityResult(result)
			require.False(t, result.Eligible)
			require.Equal(t, "Unknown", result.Grade)
		})
	}
}

func TestProxyReputationCleanEvidenceAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	data := reputationFixture(now)
	data.Sources[0].ObservedAt = now.Add(-71 * time.Hour).Format(time.RFC3339)
	reputation := assessProxyIPReputation(data, netip.MustParseAddr("8.8.8.8"), now)
	require.Equal(t, "current", reputation.Status)
	require.Equal(t, now.Add(time.Hour).Unix(), reputation.ValidUntil)
	result := &ProxyQualityCheckResult{PassedCount: 2, Items: []ProxyQualityCheckItem{
		{Target: "base_connectivity", Status: "pass"},
		{Target: "gemini", Platform: PlatformGemini, Surface: "api", Status: "pass", Evidence: "authentication_required"},
	}, Reputation: reputation}
	finalizeProxyQualityResult(result)
	require.True(t, result.Eligible)
	require.Equal(t, "healthy", proxyQualityOverallStatus(result))
	result.Items[1].Status, result.Items[1].Evidence = "challenge", "waf_challenge"
	result.ChallengeCount = 1
	finalizeProxyQualityResult(result)
	require.True(t, result.BaseEligible)
	require.False(t, result.Eligible)
	require.Equal(t, "challenge", proxyQualityOverallStatus(result))
}

func TestFetchProxyReputationFailsClosed(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusForbidden, http.StatusBadGateway, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"risk":0}`))
			}))
			defer server.Close()
			result := fetchProxyIPReputation(context.Background(), server.Client(), server.URL, "8.8.8.8", time.Now())
			require.Equal(t, "insufficient", result.Status)
			require.Nil(t, result.RiskScore)
		})
	}
}

func TestProxyQualitySnapshotBoundToExitConfigAndTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	proxy := &Proxy{Host: "8.8.8.8", Protocol: "socks5", Port: 1080}
	checked := now.Unix()
	info := &ProxyLatencyInfo{Success: true, IPAddress: proxy.Host, QualityStatus: "healthy", QualityBaseEligible: true, QualityCheckedAt: &checked, QualityValidUntil: now.Add(time.Hour).Unix(), QualityFingerprint: proxyQualityFingerprint(proxy)}
	require.True(t, proxyQualityRecommended(proxy, info, now))
	require.False(t, proxyQualityRecommended(proxy, info, now.Add(time.Hour)))
	changed := *proxy
	changed.Password = "new-password"
	require.False(t, proxyQualityRecommended(&changed, info, now))
	info.IPAddress = "1.1.1.1"
	require.False(t, proxyQualityRecommended(proxy, info, now))
	info.IPAddress = proxy.Host
	info.QualityValidUntil = 0
	require.False(t, proxyQualityRecommended(proxy, info, now))
	require.False(t, proxyQualityRecommended(proxy, nil, now))
}

func TestProxyReputationSelfLookupUsesObservedExit(t *testing.T) {
	for _, observedIP := range []string{"8.8.8.8", "1.1.1.1"} {
		t.Run(observedIP, func(t *testing.T) {
			now := time.Now()
			data := reputationFixture(now)
			data.Geo.IP = observedIP
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Model exhausted arbitrary-lookup quota while self-lookup remains available.
				if r.URL.Query().Has("ip") {
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(data)
			}))
			defer proxy.Close()
			client, err := httpclient.GetClient(httpclient.Options{ProxyURL: proxy.URL, Timeout: time.Second})
			require.NoError(t, err)
			reputation := fetchProxyIPReputation(context.Background(), client, "http://ipok.invalid/api/ip", "8.8.8.8", now)
			result := &ProxyQualityCheckResult{
				PassedCount: 1, Reputation: reputation,
				Items: []ProxyQualityCheckItem{{Target: "base_connectivity", Status: "pass"}},
			}
			finalizeProxyQualityResult(result)
			if observedIP == "8.8.8.8" {
				require.Equal(t, "current", reputation.Status)
				require.True(t, result.BaseEligible)
			} else {
				require.Equal(t, "insufficient", reputation.Status)
				require.Nil(t, reputation.RiskScore)
				require.False(t, result.Eligible)
			}
		})
	}
}

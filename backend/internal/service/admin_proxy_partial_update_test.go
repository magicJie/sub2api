//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdminProxyPartialUpdatePreservesOmittedSettings(t *testing.T) {
	for _, input := range []*UpdateProxyInput{{Status: "inactive"}, {Name: "renamed"}, {Host: "new.example"}} {
		expiry := time.Now().Add(24 * time.Hour)
		backup := int64(10)
		original := &Proxy{ID: 9, Name: "original", Host: "old.example", Status: StatusActive, ExpiresAt: &expiry, FallbackMode: FallbackModeProxy, BackupProxyID: &backup, ExpiryWarnDays: 7}
		repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: original}
		svc := &adminServiceImpl{proxyRepo: repo}
		got, err := svc.UpdateProxy(context.Background(), 9, input)
		require.NoError(t, err)
		require.Equal(t, original.ExpiresAt, got.ExpiresAt)
		require.Equal(t, original.FallbackMode, got.FallbackMode)
		require.Equal(t, original.BackupProxyID, got.BackupProxyID)
		require.Equal(t, original.ExpiryWarnDays, got.ExpiryWarnDays)
		require.Equal(t, 1, repo.updateCalls)
	}
}

func TestAdminProxyPartialUpdateClearsAndSetsSettings(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	backup := int64(10)
	zero := 0
	repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: &Proxy{ID: 9, ExpiresAt: &expiry, FallbackMode: FallbackModeProxy, BackupProxyID: &backup, ExpiryWarnDays: 7}}
	svc := &adminServiceImpl{proxyRepo: repo}
	got, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		ClearExpiresAt: true, FallbackMode: FallbackModeNone, ClearBackupID: true, ExpiryWarnDays: &zero,
	})
	require.NoError(t, err)
	require.Nil(t, got.ExpiresAt)
	require.Nil(t, got.BackupProxyID)
	require.Equal(t, FallbackModeNone, got.FallbackMode)
	require.Zero(t, got.ExpiryWarnDays)
	days := 3
	got, err = svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		ExpiresAt: &expiry, FallbackMode: FallbackModeProxy, BackupProxyID: &backup, ExpiryWarnDays: &days,
	})
	require.NoError(t, err)
	require.Equal(t, &expiry, got.ExpiresAt)
	require.Equal(t, &backup, got.BackupProxyID)
	require.Equal(t, FallbackModeProxy, got.FallbackMode)
	require.Equal(t, days, got.ExpiryWarnDays)
}

func TestAdminProxyPartialUpdateValidatesMergedFallback(t *testing.T) {
	backup := int64(10)
	self := int64(9)
	negative := -1
	for _, tc := range []struct {
		name      string
		input     UpdateProxyInput
		wantError bool
	}{
		{name: "reuse existing backup", input: UpdateProxyInput{FallbackMode: FallbackModeProxy}},
		{name: "clear required backup", input: UpdateProxyInput{ClearBackupID: true}, wantError: true},
		{name: "self backup", input: UpdateProxyInput{BackupProxyID: &self}, wantError: true},
		{name: "negative warning", input: UpdateProxyInput{ExpiryWarnDays: &negative}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: &Proxy{ID: 9, FallbackMode: FallbackModeProxy, BackupProxyID: &backup}}
			svc := &adminServiceImpl{proxyRepo: repo}
			_, err := svc.UpdateProxy(context.Background(), 9, &tc.input)
			if tc.wantError {
				require.Error(t, err)
				require.Zero(t, repo.updateCalls)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAdminProxyMoveQualityIsAdvisory(t *testing.T) {
	for _, tc := range poolQualityEvidenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			one, two := int64(1), int64(2)
			original := &Proxy{ID: 9, Protocol: "socks5", Host: "8.8.8.8", Port: 1080, Status: StatusActive, PoolID: &one}
			poolRepo := &proxyPoolRepoStub{pools: map[int64]*ProxyPool{1: {ID: 1}, 2: {ID: 2}},
				members: []ProxyPoolMember{{Proxy: *original}}}
			poolService := withProxyPoolService(t, poolRepo)
			cache := poolService.latencyCache.(*poolQualityCache)
			if tc.change != nil {
				tc.change(cache.infos[9])
			}
			if tc.missing {
				delete(cache.infos, 9)
			}
			if tc.cacheError {
				cache.err = errors.New("cache offline")
			}
			repo := &updatingProxyRepoStub{proxyRepoStub: &proxyRepoStub{}, proxy: original}
			svc := &adminServiceImpl{proxyRepo: repo, proxyLatencyCache: cache}
			if tc.noCache {
				svc.proxyLatencyCache = nil
			}
			password := "synthetic-new-password"
			_, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{PoolIDSet: true, PoolID: &two, Password: &password})
			require.NoError(t, err)
			require.Equal(t, &two, repo.proxy.PoolID)
			require.Equal(t, password, repo.proxy.Password)
			require.Zero(t, cache.reads)

			missingPool := int64(99)
			_, err = svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{PoolIDSet: true, PoolID: &missingPool})
			require.ErrorIs(t, err, ErrProxyPoolNotFound)
			require.Equal(t, &two, repo.proxy.PoolID)
		})
	}
}

func TestProxyConnectionProbeDoesNotRenewQualityEvidence(t *testing.T) {
	now := time.Now()
	proxy := Proxy{ID: 9, Protocol: "socks5", Host: "8.8.8.8", Port: 1080}
	cache := poolQualityFixture([]ProxyPoolMember{{Proxy: proxy}})
	checked := now.Add(-23 * time.Hour).Unix()
	validUntil := now.Add(time.Minute).Unix()
	cache.infos[9].QualityCheckedAt = &checked
	cache.infos[9].QualityValidUntil = validUntil
	svc := &adminServiceImpl{proxyLatencyCache: cache}
	svc.saveProxyLatency(context.Background(), 9, &ProxyLatencyInfo{
		Success: true, IPAddress: proxy.Host, UpdatedAt: now,
	})
	require.Equal(t, checked, *cache.infos[9].QualityCheckedAt)
	require.Equal(t, validUntil, cache.infos[9].QualityValidUntil)
	require.True(t, proxyQualityRecommended(&proxy, cache.infos[9], now))
	require.False(t, proxyQualityRecommended(&proxy, cache.infos[9], now.Add(time.Minute)))
}

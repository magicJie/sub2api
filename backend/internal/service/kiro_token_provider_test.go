//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type kiroTokenProviderRepo struct {
	mockAccountRepoForGemini
	setErrorCalls int
	setErrorID    int64
	setErrorMsg   string
}

func (r *kiroTokenProviderRepo) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorCalls++
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	if account := r.accountsByID[id]; account != nil {
		account.Status = StatusError
	}
	return nil
}

type kiroTokenProviderSequenceRepo struct {
	kiroTokenProviderRepo
	accounts []*Account
	reads    int
}

func (r *kiroTokenProviderSequenceRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	if len(r.accounts) == 0 {
		return nil, errors.New("account not found")
	}
	idx := r.reads
	if idx >= len(r.accounts) {
		idx = len(r.accounts) - 1
	}
	r.reads++
	return r.accounts[idx], nil
}

type stubKiroAccountTokenRefresher struct {
	tokenInfo *KiroTokenInfo
	err       error
	calls     int
	onRefresh func(*Account)
}

func (s *stubKiroAccountTokenRefresher) RefreshAccountToken(_ context.Context, account *Account) (*KiroTokenInfo, error) {
	s.calls++
	if s.onRefresh != nil {
		s.onRefresh(account)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.tokenInfo, nil
}

func (s *stubKiroAccountTokenRefresher) BuildAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	return map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   tokenInfo.ExpiresAt,
	}
}

func TestKiroTokenProviderForceRefreshInvalidGrantSetsError(t *testing.T) {
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Credentials: map[string]any{"refresh_token": "old-refresh"},
	}
	repo := &kiroTokenProviderRepo{
		mockAccountRepoForGemini: mockAccountRepoForGemini{
			accountsByID: map[int64]*Account{account.ID: snapshotOAuthRefreshAccount(account)},
		},
	}
	provider := NewKiroTokenProvider(repo, newOpenAITokenCacheStub(), nil)
	provider.kiroOAuthService = &stubKiroAccountTokenRefresher{err: errors.New("invalid_grant: token revoked")}

	token, err := provider.ForceRefreshAccessToken(context.Background(), account)
	require.Error(t, err)
	require.Empty(t, token)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, account.ID, repo.setErrorID)
	require.Equal(t, StatusError, account.Status)
	require.Equal(t, StatusError, repo.accountsByID[account.ID].Status)
}

func TestKiroTokenProviderForceRefreshRaceRecoveryDoesNotSetError(t *testing.T) {
	usedAccount := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "old-refresh"},
	}
	latestAccount := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "new-refresh", "access_token": "fresh-access", "_token_version": int64(2)},
	}
	repo := &kiroTokenProviderSequenceRepo{accounts: []*Account{usedAccount, latestAccount}}
	provider := NewKiroTokenProvider(repo, newOpenAITokenCacheStub(), nil)
	provider.kiroOAuthService = &stubKiroAccountTokenRefresher{err: errors.New("invalid_grant: token revoked")}

	token, err := provider.ForceRefreshAccessToken(context.Background(), usedAccount)
	require.NoError(t, err)
	require.Equal(t, "fresh-access", token)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, token, usedAccount.GetCredential("access_token"))
}

type kiroBusyTokenCache struct {
	geminiTokenCacheStub
}

func (*kiroBusyTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}

func TestKiroForceRefreshRespectsBusyLock(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "old", "refresh_token": "old"}}
	refresher := &stubKiroAccountTokenRefresher{}
	provider := NewKiroTokenProvider(nil, &kiroBusyTokenCache{}, nil)
	provider.kiroOAuthService = refresher
	_, err := provider.ForceRefreshAccessToken(context.Background(), account)
	require.ErrorIs(t, err, errRefreshSkipped)
	require.Zero(t, refresher.calls)
}

func TestKiroForceRefreshUpdatesCallerSnapshot(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "old", "profile_arn": "old-profile"}}
	latest := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "new", "profile_arn": "new-profile", "_token_version": int64(2)}}
	provider := NewKiroTokenProvider(&kiroTokenProviderSequenceRepo{accounts: []*Account{latest}}, &kiroBusyTokenCache{}, nil)
	refresher := &stubKiroAccountTokenRefresher{}
	provider.kiroOAuthService = refresher
	token, err := provider.ForceRefreshAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "new", token)
	require.Equal(t, token, account.GetCredential("access_token"))
	require.Equal(t, "new-profile", account.GetCredential("profile_arn"))
	require.Zero(t, refresher.calls)
}

func TestKiroGetAccessTokenUpdatesVersionedCallerProfile(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "old", "profile_arn": "old-profile"}}
	latest := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "new", "profile_arn": "new-profile", "_token_version": int64(2)}}
	provider := NewKiroTokenProvider(&kiroTokenProviderSequenceRepo{accounts: []*Account{latest}}, &geminiTokenCacheStub{}, nil)
	token, err := provider.GetAccessToken(t.Context(), account)
	require.NoError(t, err)
	require.Equal(t, "new", token)
	require.Equal(t, token, account.GetCredential("access_token"))
	require.Equal(t, "new-profile", account.GetCredential("profile_arn"))
}

func TestKiroForceRefreshBusyCacheRequiresFreshSnapshot(t *testing.T) {
	tests := []struct {
		name          string
		cacheToken    string
		databaseToken string
		version       int64
		waitForCache  bool
		missingDB     bool
		wantToken     string
	}{
		{name: "rejected token stays rejected", cacheToken: "old", databaseToken: "old", version: 1, waitForCache: true},
		{name: "newer version with rejected token stays rejected", cacheToken: "old", databaseToken: "old", version: 2, waitForCache: true},
		{name: "fresh cache with matching metadata", cacheToken: "new", databaseToken: "new", version: 1, waitForCache: true, wantToken: "new"},
		{name: "fresh cache respects policy", cacheToken: "new", databaseToken: "new", version: 1},
		{name: "fresh cache needs matching metadata", cacheToken: "new", databaseToken: "old", version: 1, waitForCache: true},
		{name: "fresh cache needs database snapshot", cacheToken: "new", waitForCache: true, missingDB: true},
		{name: "new database version needs access token", cacheToken: "", databaseToken: " ", version: 2, waitForCache: true},
		{name: "new database version supplies snapshot", cacheToken: "old", databaseToken: "new", version: 2, wantToken: "new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "old", "profile_arn": "old-profile", "_token_version": int64(1)}}
			latest := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": tt.databaseToken, "profile_arn": "new-profile", "_token_version": tt.version}}
			repo := &kiroTokenProviderSequenceRepo{}
			if !tt.missingDB {
				repo.accounts = []*Account{latest}
			}
			cache := newOpenAITokenCacheStub()
			cache.lockAcquired = false
			require.NoError(t, cache.SetAccessToken(t.Context(), KiroTokenCacheKey(account), tt.cacheToken, time.Minute))
			provider := NewKiroTokenProvider(repo, cache, nil)
			if tt.waitForCache {
				provider.SetRefreshPolicy(ProviderRefreshPolicy{OnLockHeld: ProviderLockHeldWaitForCache})
			}
			refresher := &stubKiroAccountTokenRefresher{}
			provider.kiroOAuthService = refresher

			token, err := provider.ForceRefreshAccessToken(t.Context(), account)
			if tt.wantToken == "" {
				require.ErrorIs(t, err, errRefreshSkipped)
				require.Empty(t, token)
				require.Equal(t, "old", account.GetCredential("access_token"))
				require.Equal(t, "old-profile", account.GetCredential("profile_arn"))
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantToken, token)
				require.Equal(t, token, account.GetCredential("access_token"))
				require.Equal(t, "new-profile", account.GetCredential("profile_arn"))
			}
			require.Zero(t, refresher.calls)
			require.Zero(t, repo.setErrorCalls)
		})
	}
}

func TestKiroForceRefreshLockedNewVersionSkipsOAuth(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "old", "refresh_token": "old-refresh", "profile_arn": "old-profile", "_token_version": int64(1)}}
	latest := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "new", "refresh_token": "new-refresh", "profile_arn": "new-profile", "_token_version": int64(2)}}
	repo := &kiroTokenProviderSequenceRepo{accounts: []*Account{latest}}
	cache := newOpenAITokenCacheStub()
	provider := NewKiroTokenProvider(repo, cache, nil)
	refresher := &stubKiroAccountTokenRefresher{err: errors.New("OAuth must not run")}
	provider.kiroOAuthService = refresher

	token, err := provider.ForceRefreshAccessToken(t.Context(), account)
	require.NoError(t, err)
	require.Equal(t, "new", token)
	require.Equal(t, latest.Credentials, account.Credentials)
	require.Equal(t, "new-profile", account.GetCredential("profile_arn"))
	require.Zero(t, refresher.calls)
	cached, err := cache.GetAccessToken(t.Context(), KiroTokenCacheKey(account))
	require.NoError(t, err)
	require.Equal(t, token, cached)
	account.Credentials["profile_arn"] = "caller-only"
	require.Equal(t, "new-profile", latest.GetCredential("profile_arn"))
}

func TestKiroForceRefreshLockedEmptyNewVersionDoesNotReturnToken(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "old", "_token_version": int64(1)}}
	latest := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": " ", "refresh_token": "new-refresh", "_token_version": int64(2)}}
	repo := &kiroTokenProviderSequenceRepo{accounts: []*Account{latest}}
	provider := NewKiroTokenProvider(repo, newOpenAITokenCacheStub(), nil)
	refreshErr := errors.New("OAuth temporarily unavailable")
	refresher := &stubKiroAccountTokenRefresher{
		err: refreshErr,
		onRefresh: func(used *Account) {
			require.Equal(t, "new-refresh", used.GetCredential("refresh_token"))
		},
	}
	provider.kiroOAuthService = refresher

	token, err := provider.ForceRefreshAccessToken(t.Context(), account)
	require.ErrorIs(t, err, refreshErr)
	require.Empty(t, token)
	require.Equal(t, 1, refresher.calls)
	require.Zero(t, repo.setErrorCalls)
}

func TestKiroForceRefreshLockedInvalidGrantRecoversConcurrentSnapshot(t *testing.T) {
	for _, accessToken := range []string{"fresh-access", ""} {
		t.Run("concurrent token "+accessToken, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive,
				Credentials: map[string]any{"access_token": "old", "refresh_token": "old-refresh", "profile_arn": "old-profile", "_token_version": int64(1)}}
			latest := &Account{ID: 42, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive,
				Credentials: map[string]any{"access_token": accessToken, "refresh_token": "new-refresh", "profile_arn": "new-profile", "_token_version": int64(2)}}
			repo := &kiroTokenProviderRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
				accountsByID: map[int64]*Account{account.ID: snapshotOAuthRefreshAccount(account)},
			}}
			cache := newOpenAITokenCacheStub()
			provider := NewKiroTokenProvider(repo, cache, nil)
			refreshErr := errors.New("invalid_grant: refresh token already rotated")
			refresher := &stubKiroAccountTokenRefresher{
				err: refreshErr,
				onRefresh: func(used *Account) {
					require.Equal(t, "old-refresh", used.GetCredential("refresh_token"))
					repo.accountsByID[account.ID] = latest
				},
			}
			provider.kiroOAuthService = refresher

			token, err := provider.ForceRefreshAccessToken(t.Context(), account)
			require.Equal(t, 1, refresher.calls)
			if accessToken == "" {
				require.ErrorIs(t, err, refreshErr)
				require.Empty(t, token)
				require.Equal(t, StatusError, account.Status)
				require.Equal(t, StatusError, latest.Status)
				require.Equal(t, 1, repo.setErrorCalls)
			} else {
				require.NoError(t, err)
				require.Equal(t, accessToken, token)
				require.Equal(t, latest.Credentials, account.Credentials)
				require.Equal(t, StatusActive, account.Status)
				require.Zero(t, repo.setErrorCalls)
				cached, err := cache.GetAccessToken(t.Context(), KiroTokenCacheKey(account))
				require.NoError(t, err)
				require.Equal(t, token, cached)
			}
		})
	}
}

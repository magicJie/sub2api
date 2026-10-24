//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func newKiroCredsUpdateRepo(platform string) *updateAccountCredsRepoStub {
	return &updateAccountCredsRepoStub{
		account: &Account{
			ID:       301,
			Platform: platform,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"access_token":  "at-existing",
				"refresh_token": "rt-existing",
				"email":         "user@example.com",
				"profile_arn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
				"machine_id":    "machine-1",
				"auth_method":   "social",
				"provider":      "Github",
				"region":        "us-east-1",
				"expires_at":    "2026-10-24T22:00:00+08:00",
			},
		},
	}
}

func TestUpdateAccount_KiroPreservesIdentityFieldsOnPartialCredentials(t *testing.T) {
	repo := newKiroCredsUpdateRepo(PlatformKiro)
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), 301, &UpdateAccountInput{
		Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"}},
	})
	require.NoError(t, err)

	creds := repo.account.Credentials
	require.Equal(t, "rt-existing", creds["refresh_token"])
	require.Equal(t, "user@example.com", creds["email"])
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST", creds["profile_arn"])
	require.Equal(t, "machine-1", creds["machine_id"])
	require.Equal(t, "social", creds["auth_method"])
	require.Equal(t, "Github", creds["provider"])
	require.Equal(t, "us-east-1", creds["region"])
	require.Equal(t, "2026-10-24T22:00:00+08:00", creds["expires_at"])
	require.NotNil(t, creds["model_mapping"])
}

func TestUpdateAccount_KiroExplicitValuesOverwriteIdentityFields(t *testing.T) {
	repo := newKiroCredsUpdateRepo(PlatformKiro)
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), 301, &UpdateAccountInput{
		Credentials: map[string]any{"profile_arn": "arn:aws:codewhisperer:eu-central-1:123456789012:profile/NEW", "machine_id": ""},
	})
	require.NoError(t, err)

	require.Equal(t, "arn:aws:codewhisperer:eu-central-1:123456789012:profile/NEW", repo.account.Credentials["profile_arn"])
	require.Equal(t, "", repo.account.Credentials["machine_id"])
	require.Equal(t, "user@example.com", repo.account.Credentials["email"])
}

func TestUpdateAccount_NonKiroKeepsReplaceSemantics(t *testing.T) {
	repo := newKiroCredsUpdateRepo(PlatformAnthropic)
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), 301, &UpdateAccountInput{
		Credentials: map[string]any{"base_url": "https://example.com"},
	})
	require.NoError(t, err)

	require.Equal(t, "rt-existing", repo.account.Credentials["refresh_token"])
	require.NotContains(t, repo.account.Credentials, "email")
	require.NotContains(t, repo.account.Credentials, "profile_arn")
}

//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type kiroRetireAccountRepo struct {
	accountRepoStub
	accounts map[int64]*Account
}

func (r *kiroRetireAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if a, ok := r.accounts[id]; ok {
		return a, nil
	}
	return nil, ErrAccountNotFound
}

type kiroRetireAdmin struct {
	AdminService
	deleted []int64
}

func (a *kiroRetireAdmin) DeleteAccount(_ context.Context, id int64) error {
	a.deleted = append(a.deleted, id)
	return nil
}

func TestKiroRetire_OnlyDeletesMarkedKiroAccounts(t *testing.T) {
	marked := map[string]any{KiroQuotaRejectedAtExtraKey: "2026-10-27T04:02:00Z"}
	repo := &kiroRetireAccountRepo{accounts: map[int64]*Account{
		1: {ID: 1, Platform: PlatformKiro, Extra: marked},
		2: {ID: 2, Platform: PlatformKiro, Extra: map[string]any{}}, // 标记已清除（新周期）
		3: {ID: 3, Platform: PlatformOpenAI, Extra: marked},
	}}
	admin := &kiroRetireAdmin{}
	svc := NewKiroRetireService(repo, admin)

	res, err := svc.Retire(context.Background(), []int64{1, 2, 3, 4})
	require.NoError(t, err)
	require.Equal(t, []int64{1}, res.Deleted)
	require.Equal(t, []int64{1}, admin.deleted)
	require.Len(t, res.Failed, 3)
	require.Equal(t, int64(2), res.Failed[0].AccountID)
}

package service

// Kiro（fork）一键退役：删除带「可退役」标记（kiro_quota_rejected_at）的账号。
//
// 消耗型账号被上游额度拒绝后即用完即弃。退役走普通的账号删除（软删除），
// 代理绑定历史由触发器保留，落地 IP 的 kiro 历史计数照常累计。
// 执行前逐个复核：必须是 kiro 账号且标记仍在（新周期 reset 后标记会被清除，不再退役）。

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrKiroRetireUnavailable   = infraerrors.ServiceUnavailable("KIRO_RETIRE_UNAVAILABLE", "kiro retire service is not available")
	ErrKiroAccountNotRetirable = infraerrors.BadRequest("KIRO_ACCOUNT_NOT_RETIRABLE", "account is not a kiro account marked as retirable")
)

// KiroRetirableAccount 是带「可退役」标记的 kiro 账号。
type KiroRetirableAccount struct {
	ID         int64
	Name       string
	RejectedAt time.Time
	Reason     string
}

// KiroRetireRepository 列出带「可退役」标记的 kiro 账号（未删除）。
type KiroRetireRepository interface {
	ListKiroRetirableAccounts(ctx context.Context) ([]KiroRetirableAccount, error)
}

type KiroRetireFailure struct {
	AccountID int64
	Error     string
}

type KiroRetireResult struct {
	Deleted []int64
	Failed  []KiroRetireFailure
}

type KiroRetireService struct {
	accountRepo AccountRepository
	admin       AdminService
}

func NewKiroRetireService(accountRepo AccountRepository, admin AdminService) *KiroRetireService {
	return &KiroRetireService{accountRepo: accountRepo, admin: admin}
}

func (s *KiroRetireService) ListRetirable(ctx context.Context) ([]KiroRetirableAccount, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrKiroRetireUnavailable
	}
	repo, ok := s.accountRepo.(KiroRetireRepository)
	if !ok {
		return nil, ErrKiroRetireUnavailable
	}
	return repo.ListKiroRetirableAccounts(ctx)
}

// Retire 逐个复核并删除账号；单个失败不影响其他账号。
func (s *KiroRetireService) Retire(ctx context.Context, accountIDs []int64) (*KiroRetireResult, error) {
	if s == nil || s.accountRepo == nil || s.admin == nil {
		return nil, ErrKiroRetireUnavailable
	}
	out := &KiroRetireResult{Deleted: []int64{}, Failed: []KiroRetireFailure{}}
	for _, id := range accountIDs {
		if err := s.retireOne(ctx, id); err != nil {
			out.Failed = append(out.Failed, KiroRetireFailure{AccountID: id, Error: err.Error()})
			continue
		}
		out.Deleted = append(out.Deleted, id)
	}
	return out, nil
}

func (s *KiroRetireService) retireOne(ctx context.Context, id int64) error {
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if account.Platform != PlatformKiro {
		return ErrKiroAccountNotRetirable
	}
	if at, _ := KiroQuotaRejectionFromExtra(account.Extra); at == nil {
		return ErrKiroAccountNotRetirable
	}
	return s.admin.DeleteAccount(ctx, id)
}

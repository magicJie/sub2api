package admin

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Kiro（fork）一键退役：删除带「可退役」标记的 kiro 账号，见 service/kiro_retire.go。

type kiroRetirableAccountDTO struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	RejectedAt time.Time `json:"rejected_at"`
	Reason     string    `json:"reason"`
}

type kiroRetireFailureDTO struct {
	AccountID int64  `json:"account_id"`
	Error     string `json:"error"`
}

// ListRetirableAccounts 列出全部带「可退役」标记的 kiro 账号，批量退役前展示名单。
// GET /api/v1/admin/kiro/retirable-accounts
func (h *KiroOAuthHandler) ListRetirableAccounts(c *gin.Context) {
	if h.kiroRetire == nil {
		response.ErrorFrom(c, service.ErrKiroRetireUnavailable)
		return
	}
	items, err := h.kiroRetire.ListRetirable(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]kiroRetirableAccountDTO, 0, len(items))
	for _, it := range items {
		out = append(out, kiroRetirableAccountDTO{ID: it.ID, Name: it.Name, RejectedAt: it.RejectedAt, Reason: it.Reason})
	}
	response.Success(c, out)
}

// RetireAccounts 删除指定的可退役 kiro 账号；不是 kiro 或标记已清除的账号跳过并返回原因。
// POST /api/v1/admin/kiro/retire  {"account_ids":[...]}
func (h *KiroOAuthHandler) RetireAccounts(c *gin.Context) {
	if h.kiroRetire == nil {
		response.ErrorFrom(c, service.ErrKiroRetireUnavailable)
		return
	}
	var req struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	ids := normalizeInt64IDList(req.AccountIDs)
	if len(ids) == 0 {
		response.BadRequest(c, "account_ids is required")
		return
	}
	result, err := h.kiroRetire.Retire(c.Request.Context(), ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	failed := make([]kiroRetireFailureDTO, 0, len(result.Failed))
	for _, f := range result.Failed {
		failed = append(failed, kiroRetireFailureDTO{AccountID: f.AccountID, Error: f.Error})
	}
	response.Success(c, gin.H{"deleted": result.Deleted, "failed": failed})
}

package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ScheduledTestHandler handles admin scheduled-test-plan management.
type ScheduledTestHandler struct {
	scheduledTestSvc *service.ScheduledTestService
	runner           *service.ScheduledTestRunnerService
}

// NewScheduledTestHandler creates a new ScheduledTestHandler.
func NewScheduledTestHandler(scheduledTestSvc *service.ScheduledTestService, runner *service.ScheduledTestRunnerService) *ScheduledTestHandler {
	return &ScheduledTestHandler{scheduledTestSvc: scheduledTestSvc, runner: runner}
}

type createScheduledTestPlanRequest struct {
	AccountID        int64  `json:"account_id" binding:"required"`
	ModelID          string `json:"model_id"`
	TestType         string `json:"test_type"`
	ExpectedModel    string `json:"expected_model"`
	QualityThreshold *int   `json:"quality_threshold" binding:"omitempty,min=1,max=100"`
	CronExpression   string `json:"cron_expression" binding:"required"`
	Enabled          *bool  `json:"enabled"`
	MaxResults       int    `json:"max_results"`
	AutoRecover      *bool  `json:"auto_recover"`
}

type batchCreateScheduledTestPlanRequest struct {
	AccountIDs       []int64 `json:"account_ids" binding:"required,min=1,max=1000,dive,gt=0"`
	Mode             string  `json:"mode" binding:"omitempty,oneof=create edit"`
	ModelID          string  `json:"model_id"`
	TestType         string  `json:"test_type"`
	ExpectedModel    string  `json:"expected_model"`
	QualityThreshold *int    `json:"quality_threshold" binding:"omitempty,min=1,max=100"`
	CronExpression   string  `json:"cron_expression" binding:"required"`
	Enabled          *bool   `json:"enabled"`
	MaxResults       int     `json:"max_results"`
	AutoRecover      bool    `json:"auto_recover"`
}

type updateScheduledTestPlanRequest struct {
	ModelID          *string `json:"model_id"`
	TestType         *string `json:"test_type"`
	ExpectedModel    *string `json:"expected_model"`
	QualityThreshold *int    `json:"quality_threshold"`
	CronExpression   *string `json:"cron_expression"`
	Enabled          *bool   `json:"enabled"`
	MaxResults       *int    `json:"max_results"`
	AutoRecover      *bool   `json:"auto_recover"`
}

// ListByAccount GET /admin/accounts/:id/scheduled-test-plans
func (h *ScheduledTestHandler) ListByAccount(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid account id")
		return
	}

	plans, err := h.scheduledTestSvc.ListPlansByAccount(c.Request.Context(), accountID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, plans)
}

// Create POST /admin/scheduled-test-plans
func (h *ScheduledTestHandler) Create(c *gin.Context) {
	var req createScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	plan := &service.ScheduledTestPlan{
		AccountID:      req.AccountID,
		ModelID:        req.ModelID,
		TestType:       req.TestType,
		ExpectedModel:  req.ExpectedModel,
		CronExpression: req.CronExpression,
		Enabled:        true,
		MaxResults:     req.MaxResults,
	}
	if req.QualityThreshold != nil {
		plan.QualityThreshold = *req.QualityThreshold
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.AutoRecover != nil {
		plan.AutoRecover = *req.AutoRecover
	}

	created, err := h.scheduledTestSvc.CreatePlan(c.Request.Context(), plan)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, created)
}

// BatchCreate POST /admin/scheduled-test-plans/batch
func (h *ScheduledTestHandler) BatchCreate(c *gin.Context) {
	var req batchCreateScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	plan := service.ScheduledTestPlan{
		ModelID: req.ModelID, TestType: req.TestType, ExpectedModel: req.ExpectedModel,
		CronExpression: req.CronExpression, MaxResults: req.MaxResults,
		Enabled: true, AutoRecover: req.AutoRecover,
	}
	if req.QualityThreshold != nil {
		plan.QualityThreshold = *req.QualityThreshold
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	batch := h.scheduledTestSvc.BatchCreatePlans
	if req.Mode == "edit" {
		batch = h.scheduledTestSvc.BatchUpsertPlans
	}
	result, err := batch(c.Request.Context(), req.AccountIDs, plan)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

// Update PUT /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Update(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	existing, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}

	var req updateScheduledTestPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.ModelID != nil {
		existing.ModelID = *req.ModelID
	}
	if req.TestType != nil {
		existing.TestType = *req.TestType
	}
	if req.ExpectedModel != nil {
		existing.ExpectedModel = *req.ExpectedModel
	}
	if req.QualityThreshold != nil {
		existing.QualityThreshold = *req.QualityThreshold
	}
	if req.CronExpression != nil {
		existing.CronExpression = *req.CronExpression
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.MaxResults != nil {
		existing.MaxResults = *req.MaxResults
	}
	if req.AutoRecover != nil {
		existing.AutoRecover = *req.AutoRecover
	}

	updated, err := h.scheduledTestSvc.UpdatePlan(c.Request.Context(), existing)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, updated)
}

// Delete DELETE /admin/scheduled-test-plans/:id
func (h *ScheduledTestHandler) Delete(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	if err := h.scheduledTestSvc.DeletePlan(c.Request.Context(), planID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ListResults GET /admin/scheduled-test-plans/:id/results
func (h *ScheduledTestHandler) ListResults(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid plan id")
		return
	}

	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}

	results, err := h.scheduledTestSvc.ListResults(c.Request.Context(), planID, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}

// ListLogs GET /admin/scheduled-test-results
func (h *ScheduledTestHandler) ListLogs(c *gin.Context) {
	var query struct {
		Platform          string `form:"platform"`
		Status            string `form:"status" binding:"omitempty,oneof=success warning failed running abnormal"`
		TestType          string `form:"test_type" binding:"omitempty,oneof=connectivity fingerprint quality comprehensive"`
		FingerprintStatus string `form:"fingerprint_status" binding:"omitempty,oneof=match mismatch inconclusive observed"`
		AccountID         *int64 `form:"account_id" binding:"omitempty,gt=0"`
		StartTime         string `form:"start_time"`
		EndTime           string `form:"end_time"`
		Page              int    `form:"page,default=1"`
		PageSize          int    `form:"page_size,default=50"`
	}
	if err := c.ShouldBindQuery(&query); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	filter := service.ScheduledTestLogFilter{
		Platform: query.Platform, Status: query.Status, TestType: query.TestType,
		FingerprintStatus: query.FingerprintStatus,
		Page:              max(1, query.Page), PageSize: min(200, max(1, query.PageSize)),
	}
	if query.AccountID != nil {
		filter.AccountID = *query.AccountID
	}
	for _, field := range []struct {
		name string
		raw  string
		dest **time.Time
	}{
		{"start_time", query.StartTime, &filter.Start},
		{"end_time", query.EndTime, &filter.End},
	} {
		if field.raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, field.raw)
		if err != nil {
			response.BadRequest(c, "invalid "+field.name+": expected RFC3339")
			return
		}
		*field.dest = &parsed
	}
	items, total, err := h.scheduledTestSvc.ListLogs(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	if items == nil {
		items = []*service.ScheduledTestLogItem{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": filter.Page, "page_size": filter.PageSize})
}

// RunNow POST /admin/scheduled-test-plans/:id/run executes and persists a plan immediately.
func (h *ScheduledTestHandler) RunNow(c *gin.Context) {
	planID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || planID <= 0 {
		response.BadRequest(c, "invalid plan id")
		return
	}

	plan, err := h.scheduledTestSvc.GetPlan(c.Request.Context(), planID)
	if err != nil {
		response.NotFound(c, "plan not found")
		return
	}
	result, err := h.runner.RunPlan(c.Request.Context(), plan)
	if err != nil {
		if errors.Is(err, service.ErrScheduledTestAlreadyRunning) {
			response.Error(c, http.StatusConflict, err.Error())
			return
		}
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, result)
}

package handler

import (
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BundleSubscriptionHandler exposes the opt-in bundle contract API. It is
// intentionally separate from the legacy subscription handler.
type BundleSubscriptionHandler struct {
	svc *service.BundleSubscriptionService
}

func NewBundleSubscriptionHandler(svc *service.BundleSubscriptionService) *BundleSubscriptionHandler {
	return &BundleSubscriptionHandler{svc: svc}
}

type bundlePlanResponse struct {
	ID                    int64                 `json:"id"`
	Name                  string                `json:"name"`
	Description           string                `json:"description"`
	ProductName           string                `json:"product_name"`
	Price                 float64               `json:"price"`
	OriginalPrice         *float64              `json:"original_price,omitempty"`
	Currency              string                `json:"currency"`
	ValidityDays          int                   `json:"validity_days"`
	ValidityUnit          string                `json:"validity_unit"`
	SharedDailyLimitUSD   *float64              `json:"shared_daily_limit_usd,omitempty"`
	SharedMonthlyLimitUSD *float64              `json:"shared_monthly_limit_usd,omitempty"`
	Features              string                `json:"features"`
	ForSale               bool                  `json:"for_sale"`
	SortOrder             int                   `json:"sort_order"`
	Groups                []bundleGroupResponse `json:"groups"`
}
type bundleGroupResponse struct {
	GroupID         int64    `json:"group_id"`
	Platform        string   `json:"platform,omitempty"`
	GroupName       string   `json:"group_name,omitempty"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd,omitempty"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd,omitempty"`
}

type bundleEntitlementResponse struct {
	ID              int64    `json:"id"`
	GroupID         int64    `json:"group_id"`
	Platform        string   `json:"platform"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd,omitempty"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd,omitempty"`
	DailyUsageUSD   float64  `json:"daily_usage_usd"`
	MonthlyUsageUSD float64  `json:"monthly_usage_usd"`
}

type bundleSubscriptionResponse struct {
	ID              int64                       `json:"id"`
	UserID          int64                       `json:"user_id"`
	BundlePlanID    int64                       `json:"bundle_plan_id"`
	Status          string                      `json:"status"`
	StartsAt        string                      `json:"starts_at"`
	ExpiresAt       string                      `json:"expires_at"`
	DailyUsageUSD   float64                     `json:"daily_usage_usd"`
	MonthlyUsageUSD float64                     `json:"monthly_usage_usd"`
	Plan            *bundlePlanResponse         `json:"plan,omitempty"`
	Entitlements    []bundleEntitlementResponse `json:"entitlements"`
}

func bundlePlanDTO(p *dbent.BundlePlan) bundlePlanResponse {
	out := bundlePlanResponse{ID: p.ID, Name: p.Name, Description: p.Description, ProductName: p.ProductName, Price: p.Price, OriginalPrice: p.OriginalPrice, Currency: p.Currency, ValidityDays: p.ValidityDays, ValidityUnit: p.ValidityUnit, SharedDailyLimitUSD: p.SharedDailyLimitUsd, SharedMonthlyLimitUSD: p.SharedMonthlyLimitUsd, Features: p.Features, ForSale: p.ForSale, SortOrder: p.SortOrder, Groups: []bundleGroupResponse{}}
	for _, g := range p.Edges.Groups {
		x := bundleGroupResponse{GroupID: g.GroupID, DailyLimitUSD: g.DailyLimitUsd, MonthlyLimitUSD: g.MonthlyLimitUsd}
		if g.Edges.Group != nil {
			x.Platform = g.Edges.Group.Platform
			x.GroupName = g.Edges.Group.Name
		}
		out.Groups = append(out.Groups, x)
	}
	return out
}

func bundleSubscriptionDTO(s *dbent.BundleSubscription) bundleSubscriptionResponse {
	out := bundleSubscriptionResponse{
		ID: s.ID, UserID: s.UserID, BundlePlanID: s.BundlePlanID, Status: s.Status,
		StartsAt: s.StartsAt.Format(time.RFC3339), ExpiresAt: s.ExpiresAt.Format(time.RFC3339),
		DailyUsageUSD: s.DailyUsageUsd, MonthlyUsageUSD: s.MonthlyUsageUsd,
		Entitlements: make([]bundleEntitlementResponse, 0, len(s.Edges.Entitlements)),
	}
	if s.Edges.Plan != nil {
		plan := bundlePlanDTO(s.Edges.Plan)
		out.Plan = &plan
	}
	for _, e := range s.Edges.Entitlements {
		out.Entitlements = append(out.Entitlements, bundleEntitlementResponse{ID: e.ID, GroupID: e.GroupID, Platform: e.Platform, DailyLimitUSD: e.DailyLimitUsd, MonthlyLimitUSD: e.MonthlyLimitUsd, DailyUsageUSD: e.DailyUsageUsd, MonthlyUsageUSD: e.MonthlyUsageUsd})
	}
	return out
}

func (h *BundleSubscriptionHandler) GetPlans(c *gin.Context) {
	plans, err := h.svc.ListPlansForSale(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]bundlePlanResponse, 0, len(plans))
	for _, p := range plans {
		out = append(out, bundlePlanDTO(p))
	}
	response.Success(c, out)
}
func (h *BundleSubscriptionHandler) GetMine(c *gin.Context) {
	sub, ok := requireAuth(c)
	if !ok {
		return
	}
	rows, err := h.svc.ListSubscriptions(c.Request.Context(), sub.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]bundleSubscriptionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, bundleSubscriptionDTO(row))
	}
	response.Success(c, out)
}
func (h *BundleSubscriptionHandler) CancelMine(c *gin.Context) {
	sub, ok := requireAuth(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	if err = h.svc.CancelPending(c.Request.Context(), sub.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"cancelled": true})
}

type bundlePlanRequest struct {
	service.BundlePlanInput
	Groups []service.BundleGroupInput `json:"groups"`
}

func (h *BundleSubscriptionHandler) AdminListPlans(c *gin.Context) {
	plans, err := h.svc.ListPlans(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]bundlePlanResponse, 0, len(plans))
	for _, p := range plans {
		out = append(out, bundlePlanDTO(p))
	}
	response.Success(c, out)
}

func (h *BundleSubscriptionHandler) AdminListSubscriptions(c *gin.Context) {
	var userID *int64
	if raw := c.Query("user_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "invalid user_id")
			return
		}
		userID = &id
	}
	rows, err := h.svc.ListAllSubscriptions(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]bundleSubscriptionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, bundleSubscriptionDTO(row))
	}
	response.Success(c, out)
}
func (h *BundleSubscriptionHandler) AdminCreatePlan(c *gin.Context) {
	var req bundlePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	p, err := h.svc.CreatePlan(c.Request.Context(), req.BundlePlanInput, req.Groups)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, bundlePlanDTO(p))
}
func (h *BundleSubscriptionHandler) AdminUpdatePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	var req bundlePlanRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	p, err := h.svc.UpdatePlan(c.Request.Context(), id, req.BundlePlanInput, req.Groups)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, bundlePlanDTO(p))
}
func (h *BundleSubscriptionHandler) AdminDeletePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	if err = h.svc.DeletePlan(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

type bundleAdminCancelRequest struct {
	UserID int64 `json:"user_id"`
}

type bundleAdminAssignRequest struct {
	UserID int64  `json:"user_id" binding:"required"`
	PlanID int64  `json:"plan_id" binding:"required"`
	Days   int    `json:"days"`
	Notes  string `json:"notes"`
}

type bundleAdminExtendRequest struct {
	Days int `json:"days" binding:"required"`
}

func (h *BundleSubscriptionHandler) AdminAssign(c *gin.Context) {
	var req bundleAdminAssignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	admin, ok := requireAuth(c)
	if !ok {
		return
	}
	row, err := h.svc.Assign(c.Request.Context(), req.UserID, req.PlanID, req.Days, &admin.UserID, req.Notes)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, row)
}

func (h *BundleSubscriptionHandler) AdminExtend(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	var req bundleAdminExtendRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	row, err := h.svc.Extend(c.Request.Context(), id, req.Days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, row)
}

func (h *BundleSubscriptionHandler) AdminCancel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	var req bundleAdminCancelRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err = h.svc.CancelPending(c.Request.Context(), req.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"cancelled": true})
}
func (h *BundleSubscriptionHandler) AdminReset(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	if err = h.svc.ResetUsage(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"reset": true})
}

func (h *BundleSubscriptionHandler) AdminRevoke(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}
	if err = h.svc.Revoke(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"revoked": true})
}

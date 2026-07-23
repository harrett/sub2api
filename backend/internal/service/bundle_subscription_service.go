package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/bundleplan"
	"github.com/Wei-Shaw/sub2api/ent/bundleplangroup"
	"github.com/Wei-Shaw/sub2api/ent/bundlesubscription"
	"github.com/Wei-Shaw/sub2api/ent/bundlesubscriptionentitlement"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrBundlePlanNotFound = infraerrors.NotFound("BUNDLE_PLAN_NOT_FOUND", "bundle plan not found")
	ErrBundlePlanInvalid  = infraerrors.BadRequest("BUNDLE_PLAN_INVALID", "bundle plan is invalid")
)

type BundlePlanInput struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	ProductName           string   `json:"product_name"`
	Price                 float64  `json:"price"`
	OriginalPrice         *float64 `json:"original_price,omitempty"`
	Currency              string   `json:"currency"`
	ValidityDays          int      `json:"validity_days"`
	ValidityUnit          string   `json:"validity_unit"`
	SharedDailyLimitUSD   *float64 `json:"shared_daily_limit_usd,omitempty"`
	SharedMonthlyLimitUSD *float64 `json:"shared_monthly_limit_usd,omitempty"`
	Features              string   `json:"features"`
	ForSale               bool     `json:"for_sale"`
	SortOrder             int      `json:"sort_order"`
}

type BundleGroupInput struct {
	GroupID         int64    `json:"group_id"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd,omitempty"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd,omitempty"`
}

// BundleSubscriptionService owns plan validation and payment fulfillment. It
// intentionally has no dependency on the legacy subscription service.
type BundleSubscriptionService struct {
	client  *dbent.Client
	enabled func(context.Context) bool
}

// BundleRefundState is kept only for the lifetime of a refund attempt. It
// lets a failed provider call restore exactly the contract it suspended.
type BundleRefundState struct {
	SubscriptionID int64
	Status         string
	ExpiresAt      time.Time
}

func NewBundleSubscriptionService(client *dbent.Client, enabled func(context.Context) bool) *BundleSubscriptionService {
	if enabled == nil {
		enabled = func(context.Context) bool { return false }
	}
	return &BundleSubscriptionService{client: client, enabled: enabled}
}
func (s *BundleSubscriptionService) requireEnabled(ctx context.Context) error {
	if s == nil || s.client == nil || !s.enabled(ctx) {
		return ErrBundleSubscriptionsDisabled
	}
	return nil
}

// EnsureEnabled is used by the shared payment service before it accepts a
// bundle order. This keeps the feature flag effective even on the legacy
// /payment/orders endpoint.
func (s *BundleSubscriptionService) EnsureEnabled(ctx context.Context) error {
	return s.requireEnabled(ctx)
}

func validateBundlePlanInput(in BundlePlanInput) error {
	if strings.TrimSpace(in.Name) == "" || in.Price <= 0 || math.IsNaN(in.Price) || math.IsInf(in.Price, 0) {
		return ErrBundlePlanInvalid
	}
	if in.ValidityDays <= 0 || in.ValidityDays > MaxValidityDays {
		return ErrBundlePlanInvalid
	}
	unit := strings.ToLower(strings.TrimSpace(in.ValidityUnit))
	if unit == "" {
		unit = "day"
	}
	if unit != "day" && unit != "week" && unit != "month" {
		return ErrBundlePlanInvalid
	}
	for _, v := range []*float64{in.SharedDailyLimitUSD, in.SharedMonthlyLimitUSD} {
		if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return ErrBundlePlanInvalid
		}
	}
	return nil
}

func (s *BundleSubscriptionService) ListPlansForSale(ctx context.Context) ([]*dbent.BundlePlan, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	return s.client.BundlePlan.Query().Where(bundleplan.ForSaleEQ(true)).WithGroups(func(q *dbent.BundlePlanGroupQuery) { q.WithGroup() }).Order(dbent.Asc(bundleplan.FieldSortOrder), dbent.Asc(bundleplan.FieldID)).All(ctx)
}

func (s *BundleSubscriptionService) ListPlans(ctx context.Context) ([]*dbent.BundlePlan, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	return s.client.BundlePlan.Query().WithGroups(func(q *dbent.BundlePlanGroupQuery) { q.WithGroup() }).Order(dbent.Asc(bundleplan.FieldSortOrder), dbent.Asc(bundleplan.FieldID)).All(ctx)
}

func (s *BundleSubscriptionService) GetPlan(ctx context.Context, id int64) (*dbent.BundlePlan, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	p, err := s.client.BundlePlan.Query().Where(bundleplan.IDEQ(id)).WithGroups(func(q *dbent.BundlePlanGroupQuery) { q.WithGroup() }).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, ErrBundlePlanNotFound
	}
	return p, err
}

func (s *BundleSubscriptionService) CreatePlan(ctx context.Context, in BundlePlanInput, groups []BundleGroupInput) (*dbent.BundlePlan, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if err := validateBundlePlanInput(in); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, ErrBundlePlanInvalid
	}
	for _, g := range groups {
		if err := s.validateBundleGroup(ctx, g); err != nil {
			return nil, err
		}
	}
	seenGroups := make(map[int64]struct{}, len(groups))
	for _, g := range groups {
		if _, exists := seenGroups[g.GroupID]; exists {
			return nil, ErrBundlePlanInvalid
		}
		seenGroups[g.GroupID] = struct{}{}
	}
	unit := strings.ToLower(strings.TrimSpace(in.ValidityUnit))
	if unit == "" {
		unit = "day"
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	b := tx.BundlePlan.Create().SetName(strings.TrimSpace(in.Name)).SetDescription(in.Description).SetProductName(in.ProductName).SetPrice(in.Price).SetNillableOriginalPrice(in.OriginalPrice).SetCurrency(currency).SetValidityDays(in.ValidityDays).SetValidityUnit(unit).SetNillableSharedDailyLimitUsd(in.SharedDailyLimitUSD).SetNillableSharedMonthlyLimitUsd(in.SharedMonthlyLimitUSD).SetFeatures(in.Features).SetForSale(in.ForSale).SetSortOrder(in.SortOrder)
	p, err := b.Save(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if _, err = tx.BundlePlanGroup.Create().SetBundlePlanID(p.ID).SetGroupID(g.GroupID).SetNillableDailyLimitUsd(g.DailyLimitUSD).SetNillableMonthlyLimitUsd(g.MonthlyLimitUSD).Save(ctx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, p.ID)
}

func (s *BundleSubscriptionService) UpdatePlan(ctx context.Context, id int64, in BundlePlanInput, groups []BundleGroupInput) (*dbent.BundlePlan, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if err := validateBundlePlanInput(in); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, ErrBundlePlanInvalid
	}
	for _, g := range groups {
		if err := s.validateBundleGroup(ctx, g); err != nil {
			return nil, err
		}
	}
	seenGroups := make(map[int64]struct{}, len(groups))
	for _, g := range groups {
		if _, exists := seenGroups[g.GroupID]; exists {
			return nil, ErrBundlePlanInvalid
		}
		seenGroups[g.GroupID] = struct{}{}
	}
	unit := strings.ToLower(strings.TrimSpace(in.ValidityUnit))
	if unit == "" {
		unit = "day"
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	b := tx.BundlePlan.UpdateOneID(id).SetName(strings.TrimSpace(in.Name)).SetDescription(in.Description).SetProductName(in.ProductName).SetPrice(in.Price).SetNillableOriginalPrice(in.OriginalPrice).SetCurrency(currency).SetValidityDays(in.ValidityDays).SetValidityUnit(unit).SetNillableSharedDailyLimitUsd(in.SharedDailyLimitUSD).SetNillableSharedMonthlyLimitUsd(in.SharedMonthlyLimitUSD).SetFeatures(in.Features).SetForSale(in.ForSale).SetSortOrder(in.SortOrder)
	p, err := b.Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, ErrBundlePlanNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.BundlePlanGroup.Delete().Where(bundleplangroup.BundlePlanIDEQ(id)).Exec(ctx); err != nil {
		return nil, err
	}
	for _, g := range groups {
		if _, err = tx.BundlePlanGroup.Create().SetBundlePlanID(p.ID).SetGroupID(g.GroupID).SetNillableDailyLimitUsd(g.DailyLimitUSD).SetNillableMonthlyLimitUsd(g.MonthlyLimitUSD).Save(ctx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, id)
}

func (s *BundleSubscriptionService) DeletePlan(ctx context.Context, id int64) error {
	if err := s.requireEnabled(ctx); err != nil {
		return err
	}
	n, err := s.client.BundlePlan.Delete().Where(bundleplan.IDEQ(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBundlePlanNotFound
	}
	return nil
}

func (s *BundleSubscriptionService) validateBundleGroup(ctx context.Context, in BundleGroupInput) error {
	if in.GroupID <= 0 {
		return ErrBundlePlanInvalid
	}
	g, err := s.client.Group.Query().Where(group.IDEQ(in.GroupID), group.StatusEQ(StatusActive), group.SubscriptionTypeEQ(SubscriptionTypeSubscription)).Only(ctx)
	if err != nil || g == nil {
		return ErrGroupNotSubscriptionType
	}
	for _, v := range []*float64{in.DailyLimitUSD, in.MonthlyLimitUSD} {
		if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return ErrBundlePlanInvalid
		}
	}
	return nil
}

func bundlePlanDurationDays(plan *dbent.BundlePlan) int {
	days := plan.ValidityDays
	switch plan.ValidityUnit {
	case "week":
		days *= 7
	case "month":
		days *= 30
	}
	return days
}

// Assign creates an administrator-granted contract using the same frozen
// entitlement snapshot as a paid order. It obeys the one-active/one-pending
// invariant and never creates a payment order.
func (s *BundleSubscriptionService) Assign(ctx context.Context, userID, planID int64, days int, assignedBy *int64, notes string) (*dbent.BundleSubscription, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	plan, err := tx.BundlePlan.Query().Where(bundleplan.IDEQ(planID)).WithGroups(func(q *dbent.BundlePlanGroupQuery) { q.WithGroup() }).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, ErrBundlePlanNotFound
	}
	if err != nil {
		return nil, err
	}
	if days <= 0 {
		days = bundlePlanDurationDays(plan)
	}
	if days > MaxValidityDays*30 {
		return nil, ErrBundlePlanInvalid
	}
	now := time.Now()
	active, _ := tx.BundleSubscription.Query().Where(bundlesubscription.UserIDEQ(userID), bundlesubscription.StatusEQ(BundleStatusActive)).ForUpdate().Only(ctx)
	if active != nil && !active.ExpiresAt.After(now) {
		if _, err = tx.BundleSubscription.UpdateOneID(active.ID).SetStatus(BundleStatusExpired).Save(ctx); err != nil {
			return nil, err
		}
		active = nil
	}
	if active != nil && active.BundlePlanID == planID {
		updated, updateErr := tx.BundleSubscription.UpdateOneID(active.ID).SetExpiresAt(active.ExpiresAt.AddDate(0, 0, days)).SetNillableAssignedBy(assignedBy).SetNotes(notes).Save(ctx)
		if updateErr != nil {
			return nil, updateErr
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return updated, nil
	}
	status := BundleStatusActive
	start := now
	if active != nil {
		status = BundleStatusPending
		start = active.ExpiresAt
		pending, _ := tx.BundleSubscription.Query().Where(bundlesubscription.UserIDEQ(userID), bundlesubscription.StatusEQ(BundleStatusPending)).ForUpdate().Only(ctx)
		if pending != nil {
			if pending.BundlePlanID != planID {
				return nil, ErrBundlePendingExists
			}
			updated, updateErr := tx.BundleSubscription.UpdateOneID(pending.ID).SetExpiresAt(pending.ExpiresAt.AddDate(0, 0, days)).SetNillableAssignedBy(assignedBy).SetNotes(notes).Save(ctx)
			if updateErr != nil {
				return nil, updateErr
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			return updated, nil
		}
	}
	contract, err := tx.BundleSubscription.Create().SetUserID(userID).SetBundlePlanID(planID).SetStatus(status).SetStartsAt(start).SetExpiresAt(start.AddDate(0, 0, days)).SetNillableAssignedBy(assignedBy).SetNotes(notes).Save(ctx)
	if err != nil {
		return nil, err
	}
	for _, pg := range plan.Edges.Groups {
		if _, err = tx.BundleSubscriptionEntitlement.Create().SetBundleSubscriptionID(contract.ID).SetGroupID(pg.GroupID).SetPlatform(lookupGroupPlatform(pg)).SetNillableDailyLimitUsd(pg.DailyLimitUsd).SetNillableMonthlyLimitUsd(pg.MonthlyLimitUsd).Save(ctx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return contract, nil
}

func (s *BundleSubscriptionService) Extend(ctx context.Context, id int64, days int) (*dbent.BundleSubscription, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if days <= 0 || days > MaxValidityDays*30 {
		return nil, ErrBundlePlanInvalid
	}
	contract, err := s.client.BundleSubscription.Query().Where(bundlesubscription.IDEQ(id)).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, ErrBundleSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	if contract.Status == BundleStatusRevoked {
		return nil, ErrBundleSubscriptionNotFound
	}
	now := time.Now()
	status := contract.Status
	start := contract.ExpiresAt
	startsAt := contract.StartsAt
	if !start.After(now) {
		start = now
		startsAt = now
		status = BundleStatusActive
	}
	return s.client.BundleSubscription.UpdateOneID(id).SetStatus(status).SetStartsAt(startsAt).SetExpiresAt(start.AddDate(0, 0, days)).Save(ctx)
}

// FulfillPaidOrder is idempotent and only accepts bundle_subscription orders.
// A same-plan purchase extends the active contract; a different plan becomes
// pending and is activated by ActivateDueContracts after expiry.
func (s *BundleSubscriptionService) FulfillPaidOrder(ctx context.Context, orderID int64) (*dbent.BundleSubscription, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	o, err := tx.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID), paymentorder.OrderTypeEQ(BundleSubscriptionType)).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, ErrBundleSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	// A callback can be delivered more than once. The contract's current order
	// link is the durable idempotency marker, including same-plan renewals.
	if linked, linkErr := tx.BundleSubscription.Query().Where(bundlesubscription.PaymentOrderIDEQ(orderID)).Only(ctx); linkErr == nil {
		_ = tx.Rollback()
		return linked, nil
	}
	if o.Status != payment.OrderStatusPaid && o.Status != payment.OrderStatusCompleted && o.Status != payment.OrderStatusRecharging {
		return nil, fmt.Errorf("bundle order is not paid")
	}
	p, err := tx.BundlePlan.Query().Where(bundleplan.IDEQ(paymentOrderBundlePlanID(o))).WithGroups(func(q *dbent.BundlePlanGroupQuery) { q.WithGroup() }).Only(ctx)
	if err != nil {
		return nil, ErrBundlePlanNotFound
	}
	now := time.Now()
	days := p.ValidityDays
	switch p.ValidityUnit {
	case "week":
		days *= 7
	case "month":
		days *= 30
	}
	existing, _ := tx.BundleSubscription.Query().Where(bundlesubscription.UserIDEQ(o.UserID), bundlesubscription.StatusEQ(BundleStatusActive)).ForUpdate().Only(ctx)
	if existing != nil && !existing.ExpiresAt.After(now) {
		if _, err := tx.BundleSubscription.UpdateOneID(existing.ID).SetStatus(BundleStatusExpired).Save(ctx); err != nil {
			return nil, err
		}
		existing = nil
	}
	if existing != nil && existing.BundlePlanID == p.ID && existing.ExpiresAt.After(now) {
		updated, err := tx.BundleSubscription.UpdateOneID(existing.ID).SetExpiresAt(existing.ExpiresAt.AddDate(0, 0, days)).SetPaymentOrderID(o.ID).Save(ctx)
		if err != nil {
			return nil, err
		}
		if _, err = tx.PaymentOrder.UpdateOneID(o.ID).SetBundleSubscriptionID(existing.ID).Save(ctx); err != nil {
			return nil, err
		}
		if _, err = tx.PaymentOrder.UpdateOneID(o.ID).SetStatus(payment.OrderStatusCompleted).SetCompletedAt(now).Save(ctx); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return updated, nil
	}
	if existing != nil {
		pending, _ := tx.BundleSubscription.Query().Where(bundlesubscription.UserIDEQ(o.UserID), bundlesubscription.StatusEQ(BundleStatusPending)).ForUpdate().Only(ctx)
		if pending != nil {
			if pending.BundlePlanID != p.ID {
				return nil, ErrBundlePendingExists
			}
			updated, updateErr := tx.BundleSubscription.UpdateOneID(pending.ID).SetExpiresAt(pending.ExpiresAt.AddDate(0, 0, days)).SetPaymentOrderID(o.ID).Save(ctx)
			if updateErr != nil {
				return nil, updateErr
			}
			if _, updateErr = tx.PaymentOrder.UpdateOneID(o.ID).SetBundleSubscriptionID(pending.ID).Save(ctx); updateErr != nil {
				return nil, updateErr
			}
			if _, updateErr = tx.PaymentOrder.UpdateOneID(o.ID).SetStatus(payment.OrderStatusCompleted).SetCompletedAt(now).Save(ctx); updateErr != nil {
				return nil, updateErr
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			return updated, nil
		}
	}
	start := now
	status := BundleStatusActive
	if existing != nil {
		start = existing.ExpiresAt
		status = BundleStatusPending
	}
	sub, err := tx.BundleSubscription.Create().SetUserID(o.UserID).SetBundlePlanID(p.ID).SetStatus(status).SetStartsAt(start).SetExpiresAt(start.AddDate(0, 0, days)).SetPaymentOrderID(o.ID).Save(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.PaymentOrder.UpdateOneID(o.ID).SetBundleSubscriptionID(sub.ID).Save(ctx); err != nil {
		return nil, err
	}
	for _, pg := range p.Edges.Groups {
		if _, err = tx.BundleSubscriptionEntitlement.Create().SetBundleSubscriptionID(sub.ID).SetGroupID(pg.GroupID).SetPlatform(lookupGroupPlatform(pg)).SetNillableDailyLimitUsd(pg.DailyLimitUsd).SetNillableMonthlyLimitUsd(pg.MonthlyLimitUsd).Save(ctx); err != nil {
			return nil, err
		}
	}
	if _, err = tx.PaymentOrder.UpdateOneID(o.ID).SetStatus(payment.OrderStatusCompleted).SetCompletedAt(now).Save(ctx); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return sub, nil
}

func lookupGroupPlatform(pg *dbent.BundlePlanGroup) string {
	if pg.Edges.Group != nil {
		return pg.Edges.Group.Platform
	}
	return ""
}

// BundlePlanGroup has no platform column by design; load it for entitlement
// snapshots when callers use this helper directly.
func (s *BundleSubscriptionService) LoadPlanGroups(ctx context.Context, planID int64) ([]*dbent.BundlePlanGroup, error) {
	return s.client.BundlePlanGroup.Query().Where(bundleplangroup.BundlePlanIDEQ(planID)).WithGroup().All(ctx)
}

func paymentOrderBundlePlanID(o *dbent.PaymentOrder) int64 {
	if o == nil || o.BundlePlanID == nil {
		return 0
	}
	return *o.BundlePlanID
}

func (s *BundleSubscriptionService) ActivateDueContracts(ctx context.Context) error {
	if err := s.requireEnabled(ctx); err != nil {
		return err
	}
	now := time.Now()
	if _, err := s.client.BundleSubscription.Update().Where(bundlesubscription.StatusEQ(BundleStatusActive), bundlesubscription.ExpiresAtLTE(now)).SetStatus(BundleStatusExpired).Save(ctx); err != nil {
		return err
	}
	pending, err := s.client.BundleSubscription.Query().Where(bundlesubscription.StatusEQ(BundleStatusPending), bundlesubscription.StartsAtLTE(now)).All(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		_, err = s.client.BundleSubscription.UpdateOneID(p.ID).SetStatus(BundleStatusActive).Save(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *BundleSubscriptionService) CancelPending(ctx context.Context, userID, id int64) error {
	if err := s.requireEnabled(ctx); err != nil {
		return err
	}
	n, err := s.client.BundleSubscription.Update().Where(bundlesubscription.IDEQ(id), bundlesubscription.UserIDEQ(userID), bundlesubscription.StatusEQ(BundleStatusPending)).SetStatus(BundleStatusRevoked).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBundleSubscriptionNotFound
	}
	return nil
}

func (s *BundleSubscriptionService) ListSubscriptions(ctx context.Context, userID int64) ([]*dbent.BundleSubscription, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if err := s.ActivateDueContracts(ctx); err != nil {
		return nil, err
	}
	return s.client.BundleSubscription.Query().Where(bundlesubscription.UserIDEQ(userID)).WithPlan().WithEntitlements().Order(dbent.Desc(bundlesubscription.FieldCreatedAt)).All(ctx)
}

// ListAllSubscriptions is the admin contract view. A nil userID returns all
// contracts; supplying one scopes the result for an individual user.
func (s *BundleSubscriptionService) ListAllSubscriptions(ctx context.Context, userID *int64) ([]*dbent.BundleSubscription, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	if err := s.ActivateDueContracts(ctx); err != nil {
		return nil, err
	}
	q := s.client.BundleSubscription.Query().WithPlan().WithEntitlements().Order(dbent.Desc(bundlesubscription.FieldCreatedAt))
	if userID != nil {
		q.Where(bundlesubscription.UserIDEQ(*userID))
	}
	return q.All(ctx)
}

func (s *BundleSubscriptionService) ResetUsage(ctx context.Context, id int64) error {
	if err := s.requireEnabled(ctx); err != nil {
		return err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now()
	if _, err = tx.BundleSubscription.UpdateOneID(id).SetDailyUsageUsd(0).SetMonthlyUsageUsd(0).SetDailyWindowStart(now).SetMonthlyWindowStart(now).Save(ctx); err != nil {
		if dbent.IsNotFound(err) {
			return ErrBundleSubscriptionNotFound
		}
		return err
	}
	if _, err = tx.BundleSubscriptionEntitlement.Update().Where(bundlesubscriptionentitlement.BundleSubscriptionIDEQ(id)).SetDailyUsageUsd(0).SetMonthlyUsageUsd(0).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// Revoke terminates a single contract without touching legacy subscriptions.
func (s *BundleSubscriptionService) Revoke(ctx context.Context, id int64) error {
	if err := s.requireEnabled(ctx); err != nil {
		return err
	}
	n, err := s.client.BundleSubscription.Update().Where(
		bundlesubscription.IDEQ(id),
		bundlesubscription.StatusNEQ(BundleStatusRevoked),
	).SetStatus(BundleStatusRevoked).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBundleSubscriptionNotFound
	}
	return nil
}

// SuspendForOrderRefund revokes only the contract currently linked to this
// payment order. It never touches a legacy subscription or another order's
// contract. A missing link is a safe no-op for historical orders.
func (s *BundleSubscriptionService) SuspendForOrderRefund(ctx context.Context, orderID int64) (*BundleRefundState, error) {
	if err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	var sub *dbent.BundleSubscription
	order, err := s.client.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID)).Only(ctx)
	if err == nil && order.BundleSubscriptionID != nil {
		sub, err = s.client.BundleSubscription.Get(ctx, *order.BundleSubscriptionID)
	} else {
		sub, err = s.client.BundleSubscription.Query().Where(bundlesubscription.PaymentOrderIDEQ(orderID)).Only(ctx)
	}
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	state := &BundleRefundState{SubscriptionID: sub.ID, Status: sub.Status, ExpiresAt: sub.ExpiresAt}
	if sub.Status == BundleStatusRevoked {
		return state, nil
	}
	if _, err = s.client.BundleSubscription.UpdateOneID(sub.ID).SetStatus(BundleStatusRevoked).Save(ctx); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *BundleSubscriptionService) RestoreRefundState(ctx context.Context, state *BundleRefundState) error {
	if state == nil || state.SubscriptionID == 0 {
		return nil
	}
	if err := s.requireEnabled(ctx); err != nil {
		return err
	}
	_, err := s.client.BundleSubscription.UpdateOneID(state.SubscriptionID).SetStatus(state.Status).SetExpiresAt(state.ExpiresAt).Save(ctx)
	return err
}

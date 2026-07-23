package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/bundlesubscription"
	"github.com/Wei-Shaw/sub2api/ent/bundlesubscriptionentitlement"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	BundleFeatureFlagKey   = "bundle_subscriptions_enabled"
	BundleSubscriptionType = "bundle_subscription"
	BundleStatusPending    = "pending"
	BundleStatusActive     = "active"
	BundleStatusExpired    = "expired"
	BundleStatusRevoked    = "revoked"
)

var (
	ErrBundleSubscriptionsDisabled = infraerrors.Forbidden("BUNDLE_SUBSCRIPTIONS_DISABLED", "cross-platform subscriptions are disabled")
	ErrBundleEntitlementNotFound   = infraerrors.Forbidden("BUNDLE_ENTITLEMENT_NOT_FOUND", "subscription does not include this group")
	ErrBundleSubscriptionNotFound  = infraerrors.NotFound("BUNDLE_SUBSCRIPTION_NOT_FOUND", "bundle subscription not found")
	ErrBundleQuotaExceeded         = infraerrors.TooManyRequests("BUNDLE_QUOTA_EXCEEDED", "bundle subscription quota exceeded")
	ErrBundlePendingExists         = infraerrors.Conflict("BUNDLE_PENDING_EXISTS", "a different bundle subscription is already pending")
)

// BundleEntitlement is the stable authorization result consumed by gateway
// adapters. A nil limit means unlimited for that window.
type BundleEntitlement struct {
	SubscriptionID             int64
	UserID                     int64
	GroupID                    int64
	Platform                   string
	SharedDailyLimitUSD        *float64
	SharedMonthlyLimitUSD      *float64
	DailyLimitUSD              *float64
	MonthlyLimitUSD            *float64
	EntitlementDailyUsageUSD   float64
	EntitlementMonthlyUsageUSD float64
	DailyUsageUSD              float64
	MonthlyUsageUSD            float64
	DailyWindowStart           *time.Time
	MonthlyWindowStart         *time.Time
	ExpiresAt                  time.Time
}

// EntitlementResolver is the small gateway integration point. Legacy group
// subscriptions can be adapted to this interface without changing gateway
// routing or payment providers.
type EntitlementResolver interface {
	Resolve(ctx context.Context, userID, groupID int64, platform string) (*BundleEntitlement, error)
	ReserveUsage(ctx context.Context, userID, groupID int64, platform string, costUSD float64) error
}

type bundleUsageBillingContextKey struct{}

// BundleUsageBilling is carried from API-key authentication to the deferred
// usage recorder. It deliberately contains the frozen entitlement rather than
// a plan reference, so an administrator editing a plan cannot alter an
// in-flight request's authorization target.
type BundleUsageBilling struct {
	Entitlement *BundleEntitlement
	Resolver    EntitlementResolver
}

// WithBundleUsageBilling marks a request as being billed against a bundle
// contract. Gateway services use this marker after the upstream response has
// produced the actual USD cost.
func WithBundleUsageBilling(ctx context.Context, entitlement *BundleEntitlement, resolver EntitlementResolver) context.Context {
	if ctx == nil || entitlement == nil || resolver == nil {
		return ctx
	}
	return context.WithValue(ctx, bundleUsageBillingContextKey{}, &BundleUsageBilling{
		Entitlement: entitlement,
		Resolver:    resolver,
	})
}

// BundleUsageBillingFromContext returns the bundle billing marker, if this
// request was authenticated through a bundle entitlement.
func BundleUsageBillingFromContext(ctx context.Context) (*BundleUsageBilling, bool) {
	if ctx == nil {
		return nil, false
	}
	billing, ok := ctx.Value(bundleUsageBillingContextKey{}).(*BundleUsageBilling)
	return billing, ok && billing != nil && billing.Entitlement != nil && billing.Resolver != nil
}

// BundleEntitlementResolver reads frozen entitlements and atomically reserves
// usage on the contract row. The row lock makes concurrent requests sharing a
// contract observe one consistent quota.
type BundleEntitlementResolver struct {
	client  *dbent.Client
	enabled func(context.Context) bool
}

func NewBundleEntitlementResolver(client *dbent.Client, enabled func(context.Context) bool) *BundleEntitlementResolver {
	if enabled == nil {
		enabled = func(context.Context) bool { return false }
	}
	return &BundleEntitlementResolver{client: client, enabled: enabled}
}

func (r *BundleEntitlementResolver) checkEnabled(ctx context.Context) error {
	if r == nil || r.client == nil || !r.enabled(ctx) {
		return ErrBundleSubscriptionsDisabled
	}
	return nil
}

func (r *BundleEntitlementResolver) Resolve(ctx context.Context, userID, groupID int64, platform string) (*BundleEntitlement, error) {
	if err := r.checkEnabled(ctx); err != nil {
		return nil, err
	}
	// Activation is normally performed by the scheduler; this bounded repair
	// keeps a just-due pending contract usable during scheduler lag.
	if err := r.client.BundleSubscription.Update().
		Where(bundlesubscription.StatusEQ(BundleStatusActive), bundlesubscription.ExpiresAtLTE(time.Now())).
		SetStatus(BundleStatusExpired).Exec(ctx); err != nil {
		return nil, fmt.Errorf("expire bundle contracts: %w", err)
	}
	if err := r.client.BundleSubscription.Update().
		Where(bundlesubscription.StatusEQ(BundleStatusPending), bundlesubscription.StartsAtLTE(time.Now())).
		SetStatus(BundleStatusActive).Exec(ctx); err != nil {
		return nil, fmt.Errorf("activate bundle contracts: %w", err)
	}
	platform = strings.TrimSpace(platform)
	now := time.Now()
	sub, err := r.client.BundleSubscription.Query().
		Where(
			bundlesubscription.UserIDEQ(userID),
			bundlesubscription.StatusEQ(BundleStatusActive),
			bundlesubscription.StartsAtLTE(now),
			bundlesubscription.ExpiresAtGT(now),
			bundlesubscription.HasEntitlementsWith(
				bundlesubscriptionentitlement.GroupIDEQ(groupID),
			),
		).
		WithPlan().
		WithEntitlements(func(q *dbent.BundleSubscriptionEntitlementQuery) {
			q.Where(bundlesubscriptionentitlement.GroupIDEQ(groupID))
		}).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, ErrBundleEntitlementNotFound
		}
		return nil, fmt.Errorf("resolve bundle entitlement: %w", err)
	}
	if len(sub.Edges.Entitlements) == 0 || sub.Edges.Plan == nil {
		return nil, ErrBundleEntitlementNotFound
	}
	e := sub.Edges.Entitlements[0]
	for _, candidate := range sub.Edges.Entitlements {
		if candidate.Platform == platform {
			e = candidate
			break
		}
	}
	return &BundleEntitlement{
		SubscriptionID: sub.ID, UserID: sub.UserID, GroupID: e.GroupID, Platform: e.Platform,
		SharedDailyLimitUSD:   sub.Edges.Plan.SharedDailyLimitUsd,
		SharedMonthlyLimitUSD: sub.Edges.Plan.SharedMonthlyLimitUsd,
		DailyLimitUSD:         e.DailyLimitUsd, MonthlyLimitUSD: e.MonthlyLimitUsd,
		EntitlementDailyUsageUSD: e.DailyUsageUsd, EntitlementMonthlyUsageUSD: e.MonthlyUsageUsd,
		DailyUsageUSD: sub.DailyUsageUsd, MonthlyUsageUSD: sub.MonthlyUsageUsd,
		DailyWindowStart: sub.DailyWindowStart, MonthlyWindowStart: sub.MonthlyWindowStart,
		ExpiresAt: sub.ExpiresAt,
	}, nil
}

func (r *BundleEntitlementResolver) ReserveUsage(ctx context.Context, userID, groupID int64, platform string, costUSD float64) error {
	if err := r.checkEnabled(ctx); err != nil {
		return err
	}
	if costUSD < 0 {
		return fmt.Errorf("bundle usage cost must not be negative")
	}
	platform = strings.TrimSpace(platform)
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin bundle usage transaction: %w", err)
	}
	// Rollback is intentionally unconditional. Ent returns ordinary error
	// values from early returns, so inspecting a separate local err variable
	// would otherwise leak failed transactions; rollback after Commit is safe.
	defer func() { _ = tx.Rollback() }()
	now := time.Now()
	sub, err := tx.BundleSubscription.Query().
		Where(bundlesubscription.UserIDEQ(userID), bundlesubscription.StatusEQ(BundleStatusActive), bundlesubscription.StartsAtLTE(now), bundlesubscription.ExpiresAtGT(now), bundlesubscription.HasEntitlementsWith(bundlesubscriptionentitlement.GroupIDEQ(groupID))).
		WithPlan().
		WithEntitlements(func(q *dbent.BundleSubscriptionEntitlementQuery) {
			q.Where(bundlesubscriptionentitlement.GroupIDEQ(groupID))
		}).
		ForUpdate().Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return ErrBundleEntitlementNotFound
		}
		return fmt.Errorf("lock bundle subscription: %w", err)
	}
	if sub.Edges.Plan == nil || len(sub.Edges.Entitlements) == 0 {
		return ErrBundleEntitlementNotFound
	}
	e := sub.Edges.Entitlements[0]
	if platform != "" && e.Platform != platform {
		return ErrBundleEntitlementNotFound
	}
	dailyReset := bundleWindowResetDue(sub.DailyWindowStart, now, 24*time.Hour)
	monthlyReset := bundleWindowResetDue(sub.MonthlyWindowStart, now, 30*24*time.Hour)
	dailyStart, dailyUsage := resetBundleWindow(sub.DailyWindowStart, sub.DailyUsageUsd, now, 24*time.Hour)
	monthlyStart, monthlyUsage := resetBundleWindow(sub.MonthlyWindowStart, sub.MonthlyUsageUsd, now, 30*24*time.Hour)
	entitlementDailyUsage := e.DailyUsageUsd
	entitlementMonthlyUsage := e.MonthlyUsageUsd
	if dailyReset {
		entitlementDailyUsage = 0
	}
	if monthlyReset {
		entitlementMonthlyUsage = 0
	}
	if exceeds(dailyUsage, costUSD, sub.Edges.Plan.SharedDailyLimitUsd) || exceeds(monthlyUsage, costUSD, sub.Edges.Plan.SharedMonthlyLimitUsd) || exceeds(entitlementDailyUsage, costUSD, e.DailyLimitUsd) || exceeds(entitlementMonthlyUsage, costUSD, e.MonthlyLimitUsd) {
		return ErrBundleQuotaExceeded
	}
	_, err = tx.BundleSubscription.UpdateOneID(sub.ID).
		SetDailyUsageUsd(dailyUsage + costUSD).SetMonthlyUsageUsd(monthlyUsage + costUSD).
		SetDailyWindowStart(*dailyStart).SetMonthlyWindowStart(*monthlyStart).Save(ctx)
	if err != nil {
		return fmt.Errorf("reserve bundle usage: %w", err)
	}
	if _, err = tx.BundleSubscriptionEntitlement.UpdateOneID(e.ID).
		SetDailyUsageUsd(entitlementDailyUsage + costUSD).
		SetMonthlyUsageUsd(entitlementMonthlyUsage + costUSD).Save(ctx); err != nil {
		return fmt.Errorf("reserve bundle entitlement usage: %w", err)
	}
	if dailyReset {
		if _, err = tx.BundleSubscriptionEntitlement.Update().
			Where(bundlesubscriptionentitlement.BundleSubscriptionIDEQ(sub.ID), bundlesubscriptionentitlement.IDNEQ(e.ID)).
			SetDailyUsageUsd(0).Save(ctx); err != nil {
			return fmt.Errorf("reset bundle entitlement daily usage: %w", err)
		}
	}
	if monthlyReset {
		if _, err = tx.BundleSubscriptionEntitlement.Update().
			Where(bundlesubscriptionentitlement.BundleSubscriptionIDEQ(sub.ID), bundlesubscriptionentitlement.IDNEQ(e.ID)).
			SetMonthlyUsageUsd(0).Save(ctx); err != nil {
			return fmt.Errorf("reset bundle entitlement monthly usage: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit bundle usage: %w", err)
	}
	return nil
}

// ReleaseUsage rolls back a reservation when the shared usage-billing
// idempotency store reports that the request was already applied (or the
// billing transaction fails). It is deliberately separate from ReserveUsage
// so the authorization path never exposes a release operation.
func (r *BundleEntitlementResolver) ReleaseUsage(ctx context.Context, userID, groupID int64, platform string, costUSD float64) error {
	if err := r.checkEnabled(ctx); err != nil {
		return err
	}
	if costUSD < 0 {
		return fmt.Errorf("bundle usage cost must not be negative")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin bundle usage release transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now()
	sub, err := tx.BundleSubscription.Query().Where(
		bundlesubscription.UserIDEQ(userID), bundlesubscription.StatusEQ(BundleStatusActive),
		bundlesubscription.StartsAtLTE(now), bundlesubscription.ExpiresAtGT(now),
		bundlesubscription.HasEntitlementsWith(bundlesubscriptionentitlement.GroupIDEQ(groupID)),
	).WithEntitlements(func(q *dbent.BundleSubscriptionEntitlementQuery) {
		q.Where(bundlesubscriptionentitlement.GroupIDEQ(groupID))
	}).ForUpdate().Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return ErrBundleEntitlementNotFound
		}
		return fmt.Errorf("lock bundle subscription for release: %w", err)
	}
	if len(sub.Edges.Entitlements) == 0 {
		return ErrBundleEntitlementNotFound
	}
	e := sub.Edges.Entitlements[0]
	if platform != "" && e.Platform != strings.TrimSpace(platform) {
		return ErrBundleEntitlementNotFound
	}
	dailyStart, dailyUsage := resetBundleWindow(sub.DailyWindowStart, sub.DailyUsageUsd, now, 24*time.Hour)
	monthlyStart, monthlyUsage := resetBundleWindow(sub.MonthlyWindowStart, sub.MonthlyUsageUsd, now, 30*24*time.Hour)
	dailyUsage = mathMaxZero(dailyUsage - costUSD)
	monthlyUsage = mathMaxZero(monthlyUsage - costUSD)
	if _, err = tx.BundleSubscription.UpdateOneID(sub.ID).SetDailyUsageUsd(dailyUsage).SetMonthlyUsageUsd(monthlyUsage).SetDailyWindowStart(*dailyStart).SetMonthlyWindowStart(*monthlyStart).Save(ctx); err != nil {
		return fmt.Errorf("release bundle usage: %w", err)
	}
	eDaily := e.DailyUsageUsd - costUSD
	eMonthly := e.MonthlyUsageUsd - costUSD
	if bundleWindowResetDue(sub.DailyWindowStart, now, 24*time.Hour) {
		eDaily = 0
	}
	if bundleWindowResetDue(sub.MonthlyWindowStart, now, 30*24*time.Hour) {
		eMonthly = 0
	}
	if _, err = tx.BundleSubscriptionEntitlement.UpdateOneID(e.ID).SetDailyUsageUsd(mathMaxZero(eDaily)).SetMonthlyUsageUsd(mathMaxZero(eMonthly)).Save(ctx); err != nil {
		return fmt.Errorf("release bundle entitlement usage: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit bundle usage release: %w", err)
	}
	return nil
}

func mathMaxZero(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

func resetBundleWindow(start *time.Time, usage float64, now time.Time, period time.Duration) (*time.Time, float64) {
	if bundleWindowResetDue(start, now, period) {
		return &now, 0
	}
	return start, usage
}

func bundleWindowResetDue(start *time.Time, now time.Time, period time.Duration) bool {
	return start == nil || !now.Before(start.Add(period))
}

func exceeds(used, additional float64, limit *float64) bool {
	return limit != nil && used+additional > *limit+1e-9
}

// SharedQuotaLedger is a dependency-free implementation used by unit tests
// and by non-Ent adapters. It models the same atomic shared daily/monthly and
// per-platform checks as BundleEntitlementResolver.
type SharedQuotaLedger struct {
	mu        sync.Mutex
	contracts map[int64]*ledgerContract
}

type ledgerContract struct {
	DailyLimit, MonthlyLimit *float64
	Daily, Monthly           float64
	DayStart, MonthStart     time.Time
	Entitlements             map[int64]ledgerEntitlement
	ExpiresAt                time.Time
	Active                   bool
}
type ledgerEntitlement struct {
	Platform                 string
	DailyLimit, MonthlyLimit *float64
	Daily, Monthly           float64
}

func NewSharedQuotaLedger() *SharedQuotaLedger {
	return &SharedQuotaLedger{contracts: map[int64]*ledgerContract{}}
}
func (l *SharedQuotaLedger) AddContract(id int64, expiresAt time.Time, dailyLimit, monthlyLimit *float64, entitlements map[int64]BundleEntitlement) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := &ledgerContract{DailyLimit: dailyLimit, MonthlyLimit: monthlyLimit, ExpiresAt: expiresAt, Active: true, Entitlements: map[int64]ledgerEntitlement{}}
	for groupID, e := range entitlements {
		c.Entitlements[groupID] = ledgerEntitlement{Platform: e.Platform, DailyLimit: e.DailyLimitUSD, MonthlyLimit: e.MonthlyLimitUSD}
	}
	l.contracts[id] = c
}
func (l *SharedQuotaLedger) Reserve(contractID, groupID int64, platform string, costUSD float64, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.contracts[contractID]
	if !ok || !c.Active || !now.Before(c.ExpiresAt) {
		return ErrBundleSubscriptionNotFound
	}
	e, ok := c.Entitlements[groupID]
	if !ok || e.Platform != platform {
		return ErrBundleEntitlementNotFound
	}
	dailyReset := c.DayStart.IsZero() || !now.Before(c.DayStart.Add(24*time.Hour))
	if dailyReset {
		c.DayStart, c.Daily = now, 0
	}
	monthlyReset := c.MonthStart.IsZero() || !now.Before(c.MonthStart.Add(30*24*time.Hour))
	if monthlyReset {
		c.MonthStart, c.Monthly = now, 0
	}
	if dailyReset {
		e.Daily = 0
	}
	if monthlyReset {
		e.Monthly = 0
	}
	if costUSD < 0 || exceeds(c.Daily, costUSD, c.DailyLimit) || exceeds(c.Monthly, costUSD, c.MonthlyLimit) || exceeds(e.Daily, costUSD, e.DailyLimit) || exceeds(e.Monthly, costUSD, e.MonthlyLimit) {
		return ErrBundleQuotaExceeded
	}
	c.Daily += costUSD
	c.Monthly += costUSD
	e.Daily += costUSD
	e.Monthly += costUSD
	c.Entitlements[groupID] = e
	return nil
}

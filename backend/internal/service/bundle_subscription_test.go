package service

import (
	"errors"
	"testing"
	"time"
)

func TestSharedQuotaLedgerSharesUsageAcrossGroups(t *testing.T) {
	ledger := NewSharedQuotaLedger()
	daily := 10.0
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	ledger.AddContract(1, now.Add(24*time.Hour), &daily, nil, map[int64]BundleEntitlement{
		11: {Platform: PlatformAnthropic},
		12: {Platform: PlatformOpenAI},
	})
	if err := ledger.Reserve(1, 11, PlatformAnthropic, 6, now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(1, 12, PlatformOpenAI, 4, now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(1, 11, PlatformAnthropic, .01, now); !errors.Is(err, ErrBundleQuotaExceeded) {
		t.Fatalf("expected shared quota error, got %v", err)
	}
}

func TestSharedQuotaLedgerResetsWindows(t *testing.T) {
	ledger := NewSharedQuotaLedger()
	daily := 1.0
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	ledger.AddContract(2, now.Add(48*time.Hour), &daily, nil, map[int64]BundleEntitlement{11: {Platform: PlatformAnthropic}})
	if err := ledger.Reserve(2, 11, PlatformAnthropic, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(2, 11, PlatformAnthropic, 1, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestSharedQuotaLedgerTracksPlatformCapsIndependently(t *testing.T) {
	ledger := NewSharedQuotaLedger()
	daily := 10.0
	platformCap := 2.0
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	ledger.AddContract(3, now.Add(24*time.Hour), &daily, nil, map[int64]BundleEntitlement{
		11: {Platform: PlatformAnthropic, DailyLimitUSD: &platformCap},
		12: {Platform: PlatformOpenAI, DailyLimitUSD: &platformCap},
	})
	if err := ledger.Reserve(3, 11, PlatformAnthropic, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(3, 12, PlatformOpenAI, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(3, 11, PlatformAnthropic, .01, now); !errors.Is(err, ErrBundleQuotaExceeded) {
		t.Fatalf("expected platform quota error, got %v", err)
	}
}

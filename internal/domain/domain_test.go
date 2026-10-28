package domain

import (
	"testing"
	"time"
)

func TestMoneyConversionRoundsHalfUp(t *testing.T) {
	if got := BsFromUsd(10_000, 365_214); got != 365_214 { // $100 -> Bs 3.652,14
		t.Fatalf("BsFromUsd = %d", got)
	}
	if got := UsdFromBs(365_214, 365_214); got != 10_000 {
		t.Fatalf("UsdFromBs = %d", got)
	}
	if got := UsdFromBs(100, 0); got != 0 {
		t.Fatalf("zero rate must not panic, got %d", got)
	}
}

func TestPaymentRoundingNeverLeavesADanglingCent(t *testing.T) {
	inv := Invoice{TotalUSD: 9_999, IssueRate: 365_214}
	for _, rate := range []Rate{365_214, 366_001, 412_345, 500_001} {
		bs := BsFromUsd(9_999, rate)
		calc, err := CalculatePayment(inv, rate, 0, bs)
		if err != nil {
			t.Fatalf("rate %d: %v", rate, err)
		}
		if calc.BalanceAfter != 0 {
			t.Fatalf("rate %d: balance after = %d, want 0", rate, calc.BalanceAfter)
		}
	}
}

func TestPaymentRejectsOverpayment(t *testing.T) {
	inv := Invoice{TotalUSD: 5_000, IssueRate: 365_214}
	if _, err := CalculatePayment(inv, 365_214, 6_000, 0); KindOf(err) != KindRule {
		t.Fatalf("cash over balance: %v", err)
	}
	if _, err := CalculatePayment(inv, 365_214, 0, BsFromUsd(9_000, 365_214)); KindOf(err) != KindRule {
		t.Fatalf("bs over balance: %v", err)
	}
	if _, err := CalculatePayment(inv, 365_214, -1, 0); KindOf(err) != KindInvalid {
		t.Fatalf("negative amount: %v", err)
	}
}

func TestLotPoolIsFEFOAndAtomicOnShortage(t *testing.T) {
	lots := []Lot{
		{ID: 1, ProductID: 1, ExpiresOn: time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC), Quantity: 5},
		{ID: 2, ProductID: 1, ExpiresOn: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Quantity: 3},
	}
	pool := NewLotPool(lots)
	allocs, err := pool.Take(1, 6)
	if err != nil || len(allocs) != 2 || allocs[0].Lot.ID != 2 || allocs[0].Quantity != 3 || allocs[1].Quantity != 3 {
		t.Fatalf("allocs = %+v err=%v", allocs, err)
	}
	if _, err := pool.Take(1, 3); KindOf(err) != KindRule { // only 2 left
		t.Fatalf("shortage should be a rule error, got %v", err)
	}
	if ch := pool.Changed(); len(ch) != 2 || ch[1].Quantity != 0 ||ch[0].Quantity != 2 { // lot 1 keeps 2 of 5
		t.Fatalf("a failed Take must not consume stock: %+v", ch)
	}
}

func TestNormalizeRIF(t *testing.T) {
	for in, want := range map[string]string{"j123456789": "J-12345678-9", "J-12345678-9": "J-12345678-9"} {
		if got := NormalizeRIF(in); got != want {
			t.Errorf("NormalizeRIF(%q) = %q, want %q", in, got, want)
		}
	}
	if ValidRIF("12345") {
		t.Error("short RIF must be invalid")
	}
}

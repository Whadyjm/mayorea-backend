package domain

import "time"

// ToleranceDays is the overdue grace period before credit is blocked
// (spec ERP-SPEC-DIS-01).
const ToleranceDays = 7

type CreditStatus struct {
	BalanceUSD    Cents
	OverdueUSD    Cents
	DaysOverdue   int
	AvailableUSD  Cents
	OpenOrdersUSD Cents
	Blocked       bool
}

// AssessCredit computes a customer's credit standing from their invoices and
// their credit orders still waiting to be dispatched.
func AssessCredit(c Customer, invoices []Invoice, orders []Order, today time.Time) CreditStatus {
	var st CreditStatus
	for _, inv := range invoices {
		bal := inv.Balance()
		if bal <= 0 {
			continue
		}
		st.BalanceUSD += bal
		if d := inv.DaysOverdue(today); d > 0 {
			st.OverdueUSD += bal
			st.DaysOverdue = max(st.DaysOverdue, d)
		}
	}
	for _, o := range orders {
		if o.Terms == TermsCredit && o.Status.IsOpen() {
			st.OpenOrdersUSD += o.Total()
		}
	}
	st.AvailableUSD = max(0, c.CreditLimitUSD-st.BalanceUSD-st.OpenOrdersUSD)
	st.Blocked = st.DaysOverdue > ToleranceDays
	return st
}

// ExceedsLimit reports whether used credit (balance + open orders) is over the limit.
func (s CreditStatus) ExceedsLimit(c Customer) bool {
	return s.BalanceUSD+s.OpenOrdersUSD > c.CreditLimitUSD
}

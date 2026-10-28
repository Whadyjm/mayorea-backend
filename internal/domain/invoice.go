package domain

import "time"

type Payment struct {
	ID             int64
	InvoiceID      int64
	Date           time.Time
	CashUSD        Cents
	Bs             Cents
	Rate           Rate // rate of the day the payment was received
	Reference      string
	AppliedUSD     Cents // cash + Bs converted to USD at that day's rate
	ExchangeDiffBs Cents // Bs received minus Bs value at the invoice's frozen rate
	TripID         int64 // set when collected on the street; 0 otherwise
}

type Invoice struct {
	ID         int64
	Number     int64 // fiscal correlative
	OrderID    int64
	CustomerID int64
	IssuedOn   time.Time
	DueOn      time.Time
	TotalUSD   Cents
	IssueRate  Rate // BCV rate frozen on the dispatch date
	Payments   []Payment
}

func (i Invoice) Paid() Cents {
	var t Cents
	for _, p := range i.Payments {
		t += p.AppliedUSD
	}
	return t
}

func (i Invoice) Balance() Cents { return i.TotalUSD - i.Paid() }

func (i Invoice) DaysOverdue(today time.Time) int {
	return max(0, DaysBetween(i.DueOn, today))
}

// NewInvoice bills a dispatched order, freezing the rate of the day.
func NewInvoice(o Order, c Customer, rate Rate, today time.Time) Invoice {
	days := c.CreditDays
	if o.Terms == TermsCash {
		days = 0
	}
	return Invoice{
		OrderID: o.ID, CustomerID: o.CustomerID,
		IssuedOn: DateOf(today), DueOn: AddDays(today, days),
		TotalUSD: o.Total(), IssueRate: rate,
	}
}

type PaymentCalc struct {
	BalanceBefore      Cents
	RemainingAfterCash Cents
	SuggestedBs        Cents // Bs that settle the rest at today's rate
	UsdFromBs          Cents
	BalanceAfter       Cents
	ExchangeDiffBs     Cents
}

// CalculatePayment splits a mixed payment (USD cash + Bs at today's rate).
// On a rule violation the partial calculation is returned with the error.
func CalculatePayment(inv Invoice, todayRate Rate, cashUSD, bs Cents) (PaymentCalc, error) {
	if cashUSD < 0 || bs < 0 {
		return PaymentCalc{}, Invalid("Los montos no pueden ser negativos.")
	}
	before := inv.Balance()
	remaining := before - cashUSD
	calc := PaymentCalc{
		BalanceBefore:      before,
		RemainingAfterCash: max(0, remaining),
		SuggestedBs:        BsFromUsd(max(0, remaining), todayRate),
		BalanceAfter:       before,
	}
	if remaining < 0 {
		return calc, Rule("El efectivo pasa el saldo de la factura.")
	}
	usd := UsdFromBs(bs, todayRate)
	// Bs that cover the rest at today's rate must close the invoice exactly:
	// conversion rounding cannot leave a dangling cent.
	if bs > 0 && abs(usd-remaining) <= 1 {
		usd = remaining
	}
	calc.UsdFromBs = usd
	if usd > remaining {
		return calc, Rule("Los bolívares pasan el saldo de la factura.")
	}
	calc.BalanceAfter = remaining - usd
	calc.ExchangeDiffBs = bs - BsFromUsd(usd, inv.IssueRate)
	return calc, nil
}

func abs(c Cents) Cents {
	if c < 0 {
		return -c
	}
	return c
}

// NewPayment builds the payment record for a valid calculation.
func NewPayment(inv Invoice, calc PaymentCalc, cashUSD, bs Cents, rate Rate, ref string, today time.Time) (Payment, error) {
	if cashUSD <= 0 && bs <= 0 {
		return Payment{}, Invalid("Indica cuánto pagó.")
	}
	return Payment{
		InvoiceID: inv.ID, Date: DateOf(today), CashUSD: cashUSD, Bs: bs, Rate: rate,
		Reference: ref, AppliedUSD: cashUSD + calc.UsdFromBs, ExchangeDiffBs: calc.ExchangeDiffBs,
	}, nil
}

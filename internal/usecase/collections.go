package usecase

import (
	"context"
	"strings"

	"github.com/mayorea/backend/internal/domain"
)

// CollectionService handles receivables and multi-currency payments.
type CollectionService struct {
	tx       TxManager
	invoices InvoiceRepository
	rates    RateRepository
	clock    Clock
}

func NewCollectionService(tx TxManager, i InvoiceRepository, r RateRepository, clock Clock) *CollectionService {
	return &CollectionService{tx: tx, invoices: i, rates: r, clock: clock}
}

func (s *CollectionService) ListInvoices(ctx context.Context, customerID int64, onlyPending bool) ([]domain.Invoice, error) {
	all, err := s.invoices.List(ctx, InvoiceFilter{CustomerID: customerID})
	if err != nil {
		return nil, err
	}
	if !onlyPending {
		return all, nil
	}
	pending := all[:0:0]
	for _, i := range all {
		if i.Balance() > 0 {
			pending = append(pending, i)
		}
	}
	return pending, nil
}

func (s *CollectionService) GetInvoice(ctx context.Context, id int64) (domain.Invoice, error) {
	return s.invoices.Get(ctx, id)
}

func (s *CollectionService) todayRate(ctx context.Context) (domain.Rate, error) {
	r, err := s.rates.Current(ctx)
	if err != nil {
		return 0, domain.Rule("Registra la tasa BCV del día antes de cobrar.")
	}
	return r.Rate, nil
}

// PreviewPayment shows how a mixed payment would settle, without saving it.
// A rule violation is returned together with the partial calculation.
func (s *CollectionService) PreviewPayment(ctx context.Context, invoiceID int64, cashUSD, bs domain.Cents) (domain.PaymentCalc, error) {
	inv, err := s.invoices.Get(ctx, invoiceID)
	if err != nil {
		return domain.PaymentCalc{}, err
	}
	rate, err := s.todayRate(ctx)
	if err != nil {
		return domain.PaymentCalc{}, err
	}
	return domain.CalculatePayment(inv, rate, cashUSD, bs)
}

type NewPayment struct {
	CashUSD   domain.Cents
	Bs        domain.Cents
	Reference string
	TripID    int64 // set by TripService for payments collected on the street
}

// RegisterPayment applies a mixed payment (USD cash + Bs at today's rate). The
// invoice row is locked so two simultaneous payments cannot overpay it.
func (s *CollectionService) RegisterPayment(ctx context.Context, invoiceID int64, in NewPayment) (domain.Payment, error) {
	var out domain.Payment
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		inv, err := s.invoices.GetForUpdate(ctx, invoiceID)
		if err != nil {
			return err
		}
		rate, err := s.todayRate(ctx)
		if err != nil {
			return err
		}
		calc, err := domain.CalculatePayment(inv, rate, in.CashUSD, in.Bs)
		if err != nil {
			return err
		}
		p, err := domain.NewPayment(inv, calc, in.CashUSD, in.Bs, rate, strings.TrimSpace(in.Reference), s.clock.Today())
		if err != nil {
			return err
		}
		p.TripID = in.TripID
		if err := s.invoices.AddPayment(ctx, &p); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/mayorea/backend/internal/adapter/memory"
	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/platform/clock"
	"github.com/mayorea/backend/internal/usecase"
)

type env struct {
	ctx  context.Context
	clk  *clock.Fixed
	cat  *usecase.CatalogService
	ord  *usecase.OrderService
	disp *usecase.DispatchService
	col  *usecase.CollectionService
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func newEnv(t *testing.T) *env {
	t.Helper()
	st := memory.NewStore()
	clk := &clock.Fixed{Day: day(2026, 10, 28)}
	e := &env{
		ctx: context.Background(), clk: clk,
		cat:  usecase.NewCatalogService(st.Products(), st.Lots(), st.Customers(), st.Trucks(), st.Rates(), clk),
		ord:  usecase.NewOrderService(st, st.Orders(), st.Customers(), st.Products(), st.Invoices(), clk),
		disp: usecase.NewDispatchService(st, st.Trucks(), st.Orders(), st.Customers(), st.Products(), st.Lots(), st.Invoices(), st.Rates(), clk),
		col:  usecase.NewCollectionService(st, st.Invoices(), st.Rates(), clk),
	}
	if _, err := e.cat.SetRate(e.ctx, 365_214); err != nil {
		t.Fatal(err)
	}
	return e
}

// unexpected carries an error that must fail the running test.
type unexpected struct{ err error }

// must unwraps a (value, error) pair; on error it panics with unexpected, and
// the test's deferred catch turns that into a normal test failure. (Go cannot
// pass a multi-value call plus a *testing.T as arguments, hence no t here.)
func must[T any](v T, err error) T {
	if err != nil {
		panic(unexpected{err})
	}
	return v
}

func catch(t *testing.T) {
	t.Helper()
	if r := recover(); r != nil {
		if u, ok := r.(unexpected); ok {
			t.Fatalf("unexpected error: %v", u.err)
		}
		panic(r)
	}
}

func wantKind(t *testing.T, err error, k domain.Kind) {
	t.Helper()
	if domain.KindOf(err) != k {
		t.Fatalf("want error kind %d, got %v", k, err)
	}
}

// seed creates one product (price $10), one customer on route "Norte" and a
// truck that serves that route.
func (e *env) seed(t *testing.T, stock int) (domain.Product, domain.Customer, domain.Truck) {
	t.Helper()
	p := must(e.cat.CreateProduct(e.ctx, usecase.NewProduct{Name: "Harina", Unit: domain.UnitBulk, PriceUSD: 1000}))
	if stock > 0 {
		must(e.cat.ReceiveLot(e.ctx, usecase.NewLot{ProductID: p.ID, Code: "L1", ExpiresOn: day(2027, 1, 1), Quantity: stock, Location: "A-1"}))
	}
	c := must(e.cat.CreateCustomer(e.ctx, usecase.NewCustomer{Name: "Abasto Sol", RIF: "J123456789", Route: "Norte", CreditLimitUSD: 100_000, CreditDays: 7}))
	tr := must(e.cat.CreateTruck(e.ctx, usecase.NewTruck{Name: "Camión 1", Driver: "Pedro", Routes: []string{"Norte"}}))
	return p, c, tr
}

func (e *env) order(c domain.Customer, p domain.Product, qty int, terms domain.Terms) (domain.Order, error) {
	return e.ord.Create(e.ctx, domain.OrderDraft{
		CustomerID: c.ID, Terms: terms, Channel: domain.ChannelOffice,
		Lines: []domain.DraftLine{{ProductID: p.ID, Quantity: qty}},
	})
}

func TestOrderReservesStockAndCannotOversell(t *testing.T) {
	defer catch(t)
	e := newEnv(t)
	p, c, _ := e.seed(t, 10)

	must(e.order(c, p, 8, domain.TermsCredit))

	_, err := e.order(c, p, 3, domain.TermsCredit) // only 2 left uncommitted
	wantKind(t, err, domain.KindRule)

	ps := must(e.cat.ListProducts(e.ctx))
	if ps[0].Committed != 8 || ps[0].Available() != 2 {
		t.Fatalf("committed=%d available=%d, want 8 and 2", ps[0].Committed, ps[0].Available())
	}
}

func TestRejectReleasesReservation(t *testing.T) {
	defer catch(t)
	e := newEnv(t)
	p, c, _ := e.seed(t, 10)
	o := must(e.order(c, p, 8, domain.TermsCredit))

	must(e.ord.Reject(e.ctx, o.ID))

	if ps := must(e.cat.ListProducts(e.ctx)); ps[0].Committed != 0 {
		t.Fatalf("committed=%d, want 0", ps[0].Committed)
	}
	_, err := e.ord.Approve(e.ctx, o.ID) // already rejected
	wantKind(t, err, domain.KindConflict)
}

func TestDispatchConsumesFEFOAndFreezesRate(t *testing.T) {
	defer catch(t)
	e := newEnv(t)
	p, c, tr := e.seed(t, 0)
	late := must(e.cat.ReceiveLot(e.ctx, usecase.NewLot{ProductID: p.ID, Code: "TARDE", ExpiresOn: day(2027, 6, 1), Quantity: 10, Location: "B-2"}))
	early := must(e.cat.ReceiveLot(e.ctx, usecase.NewLot{ProductID: p.ID, Code: "TEMPRANO", ExpiresOn: day(2027, 1, 1), Quantity: 4, Location: "A-1"}))

	o := must(e.order(c, p, 6, domain.TermsCredit))
	must(e.ord.Approve(e.ctx, o.ID))

	plan := must(e.disp.Plan(e.ctx, tr.ID))
	if len(plan.Lines) != 1 || plan.Lines[0].Allocations[0].Lot.ID != early.ID || plan.Lines[0].Allocations[0].Quantity != 4 {
		t.Fatalf("plan should start with the earliest-expiring lot: %+v", plan.Lines)
	}

	invs := must(e.disp.Dispatch(e.ctx, tr.ID))
	if len(invs) != 1 || invs[0].TotalUSD != 6000 || invs[0].IssueRate != 365_214 {
		t.Fatalf("unexpected invoice: %+v", invs)
	}
	if want := day(2026, 11, 4); !invs[0].DueOn.Equal(want) {
		t.Fatalf("due %v, want %v (7 credit days)", invs[0].DueOn, want)
	}

	lots := must(e.cat.ListLots(e.ctx, p.ID))
	left := map[int64]int{}
	for _, l := range lots {
		left[l.ID] = l.Quantity
	}
	if left[early.ID] != 0 || left[late.ID] != 8 {
		t.Fatalf("lots after dispatch = %v, want early=0 late=8", left)
	}
	if ps := must(e.cat.ListProducts(e.ctx)); ps[0].Committed != 0 || ps[0].Physical != 8 {
		t.Fatalf("committed=%d physical=%d, want 0 and 8", ps[0].Committed, ps[0].Physical)
	}

	_, err := e.disp.Dispatch(e.ctx, tr.ID) // nothing left to ship
	wantKind(t, err, domain.KindRule)
}

// Acceptance criterion 2: more than 7 days overdue blocks credit at once.
func TestCustomerBlockedAfterMoreThanSevenDaysOverdue(t *testing.T) {
	defer catch(t)
	e := newEnv(t)
	p, c, tr := e.seed(t, 50)
	o := must(e.order(c, p, 5, domain.TermsCredit))
	must(e.ord.Approve(e.ctx, o.ID))
	must(e.disp.Dispatch(e.ctx, tr.ID)) // due 2026-11-04

	e.clk.Day = day(2026, 11, 11) // 7 days overdue: still inside the tolerance
	if st := must(e.ord.CustomerCredit(e.ctx, c.ID)); st.Blocked {
		t.Fatalf("7 days overdue must not block: %+v", st)
	}

	e.clk.Day = day(2026, 11, 12) // 8 days overdue
	st := must(e.ord.CustomerCredit(e.ctx, c.ID))
	if !st.Blocked || st.DaysOverdue != 8 {
		t.Fatalf("8 days overdue must block: %+v", st)
	}
	_, err := e.order(c, p, 1, domain.TermsCredit)
	wantKind(t, err, domain.KindRule)
	must(e.order(c, p, 1, domain.TermsCash)) // cash still allowed
}

// Acceptance criterion 3: $100 invoice, $40 cash and the rest in Bs at the
// day's rate, settled to the cent, with the exchange difference recorded.
func TestMixedPaymentSettlesToTheCent(t *testing.T) {
	defer catch(t)
	e := newEnv(t)
	p, c, tr := e.seed(t, 200)
	o := must(e.order(c, p, 10, domain.TermsCredit)) // 10 x $10 = $100
	must(e.ord.Approve(e.ctx, o.ID))
	inv := must(e.disp.Dispatch(e.ctx, tr.ID))[0]
	if inv.TotalUSD != 10_000 {
		t.Fatalf("invoice total %d, want 10000", inv.TotalUSD)
	}

	must(e.cat.SetRate(e.ctx, 400_000)) // the rate moved to 40,0000 Bs/$

	calc := must(e.col.PreviewPayment(e.ctx, inv.ID, 4000, 0))
	if calc.SuggestedBs != 240_000 { // $60 x 40 = Bs 2.400,00
		t.Fatalf("suggested Bs = %d, want 240000", calc.SuggestedBs)
	}

	pay := must(e.col.RegisterPayment(e.ctx, inv.ID, usecase.NewPayment{CashUSD: 4000, Bs: calc.SuggestedBs, Reference: "PM-001"}))
	if pay.AppliedUSD != 10_000 {
		t.Fatalf("applied %d, want 10000", pay.AppliedUSD)
	}
	// Bs received minus the value of $60 at the frozen invoice rate (36,5214).
	if want := domain.Cents(240_000 - 219_128); pay.ExchangeDiffBs != want {
		t.Fatalf("exchange diff %d, want %d", pay.ExchangeDiffBs, want)
	}
	if got := must(e.col.GetInvoice(e.ctx, inv.ID)); got.Balance() != 0 {
		t.Fatalf("balance %d, want 0", got.Balance())
	}

	_, err := e.col.RegisterPayment(e.ctx, inv.ID, usecase.NewPayment{CashUSD: 100})
	wantKind(t, err, domain.KindRule) // already settled: cannot overpay
}

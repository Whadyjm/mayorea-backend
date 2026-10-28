package usecase_test

import (
	"testing"

	"github.com/mayorea/backend/internal/adapter/memory"
	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/platform/clock"
	"github.com/mayorea/backend/internal/usecase"
)

type tripEnv struct {
	*env
	trips *usecase.TripService
}

func newTripEnv(t *testing.T) *tripEnv {
	t.Helper()
	st := memory.NewStore()
	clk := &clock.Fixed{Day: day(2026, 10, 28)}
	e := &env{
		ctx: t.Context(), clk: clk,
		cat:  usecase.NewCatalogService(st.Products(), st.Lots(), st.Customers(), st.Trucks(), st.Rates(), clk),
		ord:  usecase.NewOrderService(st, st.Orders(), st.Customers(), st.Products(), st.Invoices(), clk),
		disp: usecase.NewDispatchService(st, st.Trucks(), st.Orders(), st.Customers(), st.Products(), st.Lots(), st.Invoices(), st.Rates(), clk),
		col:  usecase.NewCollectionService(st, st.Invoices(), st.Rates(), clk),
	}
	must(t, e.cat.SetRate(e.ctx, 365_214))
	trips := usecase.NewTripService(st, st.Trips(), st.Trucks(), st.Orders(), st.Customers(), st.Products(), st.Invoices(),
		st.Lots(), st.Rates(), e.disp, e.ord, e.col, clk)
	return &tripEnv{env: e, trips: trips}
}

func stockOf(d usecase.TripDetail, productID int64) domain.TruckStock {
	for _, s := range d.Stock {
		if s.ProductID == productID {
			return s
		}
	}
	return domain.TruckStock{}
}

// The whole day of a truck: leave with one order plus extra load, deliver, sell
// from the truck, take a presale, collect, close, and get settled.
func TestTripFullCycle(t *testing.T) {
	e := newTripEnv(t)
	p, c, tr := e.seed(t, 100)
	o := must(t, e.order(c, p, 10, domain.TermsCredit))
	must(t, e.ord.Approve(e.ctx, o.ID))

	trip := must(t, e.trips.Depart(e.ctx, tr.ID, []domain.Qty{{ProductID: p.ID, Quantity: 20}}))
	if len(trip.Orders) != 1 || len(trip.Load) != 1 || trip.Load[0].Quantity != 30 {
		t.Fatalf("trip load should be 10 (order) + 20 (extra): %+v", trip)
	}
	if len(trip.Stops) != 1 || trip.Stops[0] != c.ID {
		t.Fatalf("stops = %v", trip.Stops)
	}
	if ps := must(t, e.cat.ListProducts(e.ctx)); ps[0].Physical != 70 || ps[0].Committed != 0 {
		t.Fatalf("warehouse physical=%d committed=%d, want 70 and 0", ps[0].Physical, ps[0].Committed)
	}

	// Free stock on the truck excludes what is promised to the order.
	d := must(t, e.trips.Detail(e.ctx, trip.ID))
	if s := stockOf(d, p.ID); s.OnBoard != 30 || s.Free != 20 {
		t.Fatalf("stock = %+v, want onBoard 30 free 20", s)
	}
	_, _, err := e.trips.Sell(e.ctx, trip.ID, usecase.RouteSale{CustomerID: c.ID, Terms: domain.TermsCash, Lines: []domain.Qty{{ProductID: p.ID, Quantity: 21}}})
	wantKind(t, err, domain.KindRule)

	must(t, e.trips.Deliver(e.ctx, trip.ID, o.ID))
	_, err = e.trips.Deliver(e.ctx, trip.ID, o.ID)
	wantKind(t, err, domain.KindRule) // already delivered

	// Sell 5 for cash and collect it on the spot.
	_, inv, err := e.trips.Sell(e.ctx, trip.ID, usecase.RouteSale{CustomerID: c.ID, Terms: domain.TermsCash, Lines: []domain.Qty{{ProductID: p.ID, Quantity: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if inv.TotalUSD != 5000 || !inv.DueOn.Equal(inv.IssuedOn) {
		t.Fatalf("cash sale invoice: %+v", inv)
	}
	must(t, e.trips.Collect(e.ctx, trip.ID, inv.ID, usecase.NewPayment{CashUSD: 5000}))

	// A presale reserves warehouse stock but does not touch the truck.
	must(t, e.trips.Presale(e.ctx, trip.ID, domain.OrderDraft{CustomerID: c.ID, Terms: domain.TermsCredit, Lines: []domain.DraftLine{{ProductID: p.ID, Quantity: 3}}}))
	if ps := must(t, e.cat.ListProducts(e.ctx)); ps[0].Committed != 3 {
		t.Fatalf("presale should commit 3, got %d", ps[0].Committed)
	}

	// 30 loaded - 10 delivered - 5 sold = 15 must come back. Driver reports 14.
	d = must(t, e.trips.Detail(e.ctx, trip.ID))
	if len(d.Summary.Return) != 1 || d.Summary.Return[0].Quantity != 15 || d.Summary.CashUSD != 5000 {
		t.Fatalf("summary = %+v", d.Summary)
	}
	closed := must(t, e.trips.Close(e.ctx, trip.ID, usecase.CloseInput{CashUSD: 4500, Returned: []domain.Qty{{ProductID: p.ID, Quantity: 14}}}))
	diff := closed.Differences
	if diff == nil || diff.Balanced || diff.CashUSD != -500 || diff.Goods[0].Difference != -1 {
		t.Fatalf("differences = %+v", diff)
	}

	// Once closed the driver can no longer operate.
	_, _, err = e.trips.Sell(e.ctx, trip.ID, usecase.RouteSale{CustomerID: c.ID, Terms: domain.TermsCash, Lines: []domain.Qty{{ProductID: p.ID, Quantity: 1}}})
	wantKind(t, err, domain.KindRule)
	_, err = e.trips.Depart(e.ctx, tr.ID, nil) // waiting for settlement
	wantKind(t, err, domain.KindRule)

	// The office approves: the 14 returned go back to the lot they left from.
	must(t, e.trips.ApproveSettlement(e.ctx, trip.ID))
	if ps := must(t, e.cat.ListProducts(e.ctx)); ps[0].Physical != 70+14 {
		t.Fatalf("physical after settlement = %d, want 84", ps[0].Physical)
	}
	_, err = e.trips.ApproveSettlement(e.ctx, trip.ID)
	wantKind(t, err, domain.KindRule) // cannot settle twice
}

func TestCloseRequiresAllDeliveries(t *testing.T) {
	e := newTripEnv(t)
	p, c, tr := e.seed(t, 10)
	o := must(t, e.order(c, p, 2, domain.TermsCash))
	must(t, e.ord.Approve(e.ctx, o.ID))
	trip := must(t, e.trips.Depart(e.ctx, tr.ID, nil))

	_, err := e.trips.Close(e.ctx, trip.ID, usecase.CloseInput{})
	wantKind(t, err, domain.KindRule)
}

func TestRouteSaleHonoursCreditRules(t *testing.T) {
	e := newTripEnv(t)
	p, c, tr := e.seed(t, 100)
	trip := must(t, e.trips.Depart(e.ctx, tr.ID, []domain.Qty{{ProductID: p.ID, Quantity: 50}}))

	// Credit limit is $1000; 50 units x $10 = $500 fits.
	_, _, err := e.trips.Sell(e.ctx, trip.ID, usecase.RouteSale{CustomerID: c.ID, Terms: domain.TermsCredit, Lines: []domain.Qty{{ProductID: p.ID, Quantity: 50}}})
	if err != nil {
		t.Fatalf("$500 on credit is within the limit: %v", err)
	}
	// Extra-only departure left nothing else to sell.
	_, _, err = e.trips.Sell(e.ctx, trip.ID, usecase.RouteSale{CustomerID: c.ID, Terms: domain.TermsCash, Lines: []domain.Qty{{ProductID: p.ID, Quantity: 1}}})
	wantKind(t, err, domain.KindRule)
}

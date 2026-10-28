package usecase

import (
	"context"

	"github.com/mayorea/backend/internal/domain"
)

// DispatchService plans truck loads (consolidated picking, FEFO) and
// dispatches them, billing every order at the rate of the departure day.
type DispatchService struct {
	tx        TxManager
	trucks    TruckRepository
	orders    OrderRepository
	customers CustomerRepository
	products  ProductRepository
	lots      LotRepository
	invoices  InvoiceRepository
	rates     RateRepository
	clock     Clock
}

func NewDispatchService(tx TxManager, t TruckRepository, o OrderRepository, c CustomerRepository,
	p ProductRepository, l LotRepository, i InvoiceRepository, r RateRepository, clock Clock) *DispatchService {
	return &DispatchService{tx: tx, trucks: t, orders: o, customers: c, products: p, lots: l, invoices: i, rates: r, clock: clock}
}

type LoadPlan struct {
	Truck  domain.Truck
	Orders []domain.Order
	Lines  []domain.LoadLine
}

func (s *DispatchService) approvedFor(ctx context.Context, truck domain.Truck) ([]domain.Order, map[int64]domain.Customer, error) {
	approved, err := s.orders.List(ctx, OrderFilter{Statuses: []domain.OrderStatus{domain.StatusApproved}})
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int64, 0, len(approved))
	for _, o := range approved {
		ids = append(ids, o.CustomerID)
	}
	customers, err := s.customers.FindByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	return domain.OrdersForTruck(truck, approved, customers), customers, nil
}

func productIDs(orders []domain.Order, extra []domain.Qty) []int64 {
	seen := map[int64]bool{}
	var ids []int64
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, o := range orders {
		for _, l := range o.Lines {
			add(l.ProductID)
		}
	}
	for _, q := range extra {
		add(q.ProductID)
	}
	return ids
}

// Plan returns the consolidated picking list for a truck without touching stock.
func (s *DispatchService) Plan(ctx context.Context, truckID int64) (LoadPlan, error) {
	truck, err := s.trucks.Get(ctx, truckID)
	if err != nil {
		return LoadPlan{}, err
	}
	orders, _, err := s.approvedFor(ctx, truck)
	if err != nil {
		return LoadPlan{}, err
	}
	lots, err := s.lots.ListAvailable(ctx, productIDs(orders, nil))
	if err != nil {
		return LoadPlan{}, err
	}
	return LoadPlan{Truck: truck, Orders: orders, Lines: domain.PlanLoad(orders, lots)}, nil
}

// ShipResult is what leaves the warehouse on a truck.
type ShipResult struct {
	Orders   []domain.Order
	Invoices []domain.Invoice
	Takes    []domain.LotTake // lots the extra (non-order) load came from
}

// Ship sends the truck's approved orders plus an optional extra load to sell
// on the street: consumes lots by FEFO, releases the reservations, bills each
// order with today's rate frozen on the invoice and marks them dispatched.
// All or nothing. It must run inside a transaction (callers wrap it).
func (s *DispatchService) Ship(ctx context.Context, truck domain.Truck, extra []domain.Qty) (ShipResult, error) {
	extra = domain.SumQty(extra)
	orders, customers, err := s.approvedFor(ctx, truck)
	if err != nil {
		return ShipResult{}, err
	}
	if len(orders) == 0 && len(extra) == 0 {
		return ShipResult{}, domain.Rule("No hay nada que cargar en %s.", truck.Name)
	}
	var rate domain.ExchangeRate
	if len(orders) > 0 {
		if rate, err = s.rates.Current(ctx); err != nil {
			return ShipResult{}, domain.Rule("Registra la tasa BCV del día antes de despachar.")
		}
	}

	ids := productIDs(orders, extra)
	products, err := s.products.LockByIDs(ctx, ids)
	if err != nil {
		return ShipResult{}, err
	}
	// Extra load comes out of free stock, not out of what is promised to orders.
	for _, q := range extra {
		p, ok := products[q.ProductID]
		if !ok {
			return ShipResult{}, domain.NotFound("Producto no encontrado.")
		}
		if free := p.Available(); q.Quantity > free {
			return ShipResult{}, domain.Rule("Solo quedan %d %ss libres de %s en el galpón.", max(free, 0), p.Unit, p.Name)
		}
	}
	lots, err := s.lots.LockAvailable(ctx, ids)
	if err != nil {
		return ShipResult{}, err
	}
	pool := domain.NewLotPool(lots)
	today := s.clock.Today()

	// Validate and compute everything before writing anything.
	built := make([]domain.Invoice, 0, len(orders))
	released := map[int64]int{}
	for _, o := range orders {
		for _, l := range o.Lines {
			if _, err := pool.Take(l.ProductID, l.Quantity); err != nil {
				return ShipResult{}, err
			}
			released[l.ProductID] += l.Quantity
		}
		built = append(built, domain.NewInvoice(o, customers[o.CustomerID], rate.Rate, today))
	}
	var takes []domain.LotTake
	for _, q := range extra {
		allocs, err := pool.Take(q.ProductID, q.Quantity)
		if err != nil {
			return ShipResult{}, err
		}
		for _, a := range allocs {
			takes = append(takes, domain.LotTake{LotID: a.Lot.ID, ProductID: q.ProductID, Quantity: a.Quantity, ExpiresOn: a.Lot.ExpiresOn})
		}
	}

	for _, l := range pool.Changed() {
		if err := s.lots.SetQuantity(ctx, l.ID, l.Quantity); err != nil {
			return ShipResult{}, err
		}
	}
	for pid, qty := range released {
		if err := s.products.AddCommitted(ctx, pid, -qty); err != nil {
			return ShipResult{}, err
		}
	}
	for i := range built {
		if err := s.invoices.Create(ctx, &built[i]); err != nil {
			return ShipResult{}, err
		}
		if err := s.orders.UpdateStatus(ctx, orders[i].ID, domain.StatusApproved, domain.StatusDispatched); err != nil {
			return ShipResult{}, err
		}
	}
	return ShipResult{Orders: orders, Invoices: built, Takes: takes}, nil
}

// Dispatch ships every approved order of the truck's routes (no extra load).
func (s *DispatchService) Dispatch(ctx context.Context, truckID int64) ([]domain.Invoice, error) {
	var res ShipResult
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		truck, err := s.trucks.Get(ctx, truckID)
		if err != nil {
			return err
		}
		res, err = s.Ship(ctx, truck, nil)
		return err
	})
	return res.Invoices, err
}

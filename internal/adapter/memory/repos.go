package memory

import (
	"context"
	"slices"
	"sort"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

// ---------- products ----------

type productRepo struct{ s *Store }

func (r productRepo) Create(ctx context.Context, p *domain.Product) error {
	defer r.s.lock(ctx)()
	p.ID = r.s.next("product")
	r.s.products[p.ID] = *p
	return nil
}

func (r productRepo) List(ctx context.Context) ([]domain.Product, error) {
	defer r.s.lock(ctx)()
	out := make([]domain.Product, 0, len(r.s.products))
	for id := range r.s.products {
		p, _ := r.s.product(id)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r productRepo) FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Product, error) {
	defer r.s.lock(ctx)()
	out := map[int64]domain.Product{}
	for _, id := range ids {
		if p, ok := r.s.product(id); ok {
			out[id] = p
		}
	}
	return out, nil
}

// LockByIDs needs no row locks: Do already serializes everything.
func (r productRepo) LockByIDs(ctx context.Context, ids []int64) (map[int64]domain.Product, error) {
	return r.FindByIDs(ctx, ids)
}

func (r productRepo) AddCommitted(ctx context.Context, id int64, delta int) error {
	defer r.s.lock(ctx)()
	p, ok := r.s.products[id]
	if !ok {
		return domain.NotFound("Producto no encontrado.")
	}
	p.Committed += delta
	r.s.products[id] = p
	return nil
}

// ---------- lots ----------

type lotRepo struct{ s *Store }

func (r lotRepo) Create(ctx context.Context, l *domain.Lot) error {
	defer r.s.lock(ctx)()
	l.ID = r.s.next("lot")
	r.s.lots[l.ID] = *l
	return nil
}

func (r lotRepo) list(match func(domain.Lot) bool) []domain.Lot {
	var out []domain.Lot
	for _, l := range r.s.lots {
		if match(l) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r lotRepo) ListByProduct(ctx context.Context, productID int64) ([]domain.Lot, error) {
	defer r.s.lock(ctx)()
	return r.list(func(l domain.Lot) bool { return l.ProductID == productID }), nil
}

func (r lotRepo) ListAvailable(ctx context.Context, ids []int64) ([]domain.Lot, error) {
	defer r.s.lock(ctx)()
	return r.list(func(l domain.Lot) bool { return l.Quantity > 0 && slices.Contains(ids, l.ProductID) }), nil
}

func (r lotRepo) LockAvailable(ctx context.Context, ids []int64) ([]domain.Lot, error) {
	return r.ListAvailable(ctx, ids)
}

func (r lotRepo) SetQuantity(ctx context.Context, id int64, qty int) error {
	defer r.s.lock(ctx)()
	l, ok := r.s.lots[id]
	if !ok {
		return domain.NotFound("Lote no encontrado.")
	}
	l.Quantity = qty
	r.s.lots[id] = l
	return nil
}

func (r lotRepo) AddQuantity(ctx context.Context, id int64, delta int) error {
	defer r.s.lock(ctx)()
	l, ok := r.s.lots[id]
	if !ok {
		return domain.NotFound("Lote no encontrado.")
	}
	l.Quantity += delta
	r.s.lots[id] = l
	return nil
}

// ---------- customers ----------

type customerRepo struct{ s *Store }

func (r customerRepo) Create(ctx context.Context, c *domain.Customer) error {
	defer r.s.lock(ctx)()
	c.ID = r.s.next("customer")
	r.s.customers[c.ID] = *c
	return nil
}

func (r customerRepo) List(ctx context.Context) ([]domain.Customer, error) {
	defer r.s.lock(ctx)()
	out := make([]domain.Customer, 0, len(r.s.customers))
	for _, c := range r.s.customers {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r customerRepo) Get(ctx context.Context, id int64) (domain.Customer, error) {
	defer r.s.lock(ctx)()
	c, ok := r.s.customers[id]
	if !ok {
		return domain.Customer{}, domain.NotFound("Cliente no encontrado.")
	}
	return c, nil
}

func (r customerRepo) FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Customer, error) {
	defer r.s.lock(ctx)()
	out := map[int64]domain.Customer{}
	for _, id := range ids {
		if c, ok := r.s.customers[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

// ---------- orders ----------

type orderRepo struct{ s *Store }

func cloneOrder(o domain.Order) domain.Order {
	o.Lines = slices.Clone(o.Lines)
	return o
}

func (r orderRepo) Create(ctx context.Context, o *domain.Order) error {
	defer r.s.lock(ctx)()
	o.ID = r.s.next("order")
	o.Number = o.ID
	r.s.orders[o.ID] = cloneOrder(*o)
	return nil
}

func (r orderRepo) Get(ctx context.Context, id int64) (domain.Order, error) {
	defer r.s.lock(ctx)()
	o, ok := r.s.orders[id]
	if !ok {
		return domain.Order{}, domain.NotFound("Pedido no encontrado.")
	}
	return cloneOrder(o), nil
}

func (r orderRepo) List(ctx context.Context, f usecase.OrderFilter) ([]domain.Order, error) {
	defer r.s.lock(ctx)()
	var out []domain.Order
	for _, o := range r.s.orders {
		if f.CustomerID != 0 && o.CustomerID != f.CustomerID {
			continue
		}
		if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, o.Status) {
			continue
		}
		out = append(out, cloneOrder(o))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r orderRepo) FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Order, error) {
	defer r.s.lock(ctx)()
	out := map[int64]domain.Order{}
	for _, id := range ids {
		if o, ok := r.s.orders[id]; ok {
			out[id] = cloneOrder(o)
		}
	}
	return out, nil
}

func (r orderRepo) UpdateStatus(ctx context.Context, id int64, from, to domain.OrderStatus) error {
	defer r.s.lock(ctx)()
	o, ok := r.s.orders[id]
	if !ok {
		return domain.NotFound("Pedido no encontrado.")
	}
	if o.Status != from {
		return domain.Conflict("El pedido ya no está en estado %q.", from)
	}
	o.Status = to
	r.s.orders[id] = o
	return nil
}

// ---------- invoices ----------

type invoiceRepo struct{ s *Store }

func cloneInvoice(i domain.Invoice) domain.Invoice {
	i.Payments = slices.Clone(i.Payments)
	return i
}

func (r invoiceRepo) Create(ctx context.Context, i *domain.Invoice) error {
	defer r.s.lock(ctx)()
	i.ID = r.s.next("invoice")
	i.Number = i.ID
	r.s.invoices[i.ID] = cloneInvoice(*i)
	return nil
}

func (r invoiceRepo) Get(ctx context.Context, id int64) (domain.Invoice, error) {
	defer r.s.lock(ctx)()
	i, ok := r.s.invoices[id]
	if !ok {
		return domain.Invoice{}, domain.NotFound("Factura no encontrada.")
	}
	return cloneInvoice(i), nil
}

func (r invoiceRepo) GetForUpdate(ctx context.Context, id int64) (domain.Invoice, error) {
	return r.Get(ctx, id)
}

func (r invoiceRepo) List(ctx context.Context, f usecase.InvoiceFilter) ([]domain.Invoice, error) {
	defer r.s.lock(ctx)()
	var out []domain.Invoice
	for _, i := range r.s.invoices {
		if f.CustomerID == 0 || i.CustomerID == f.CustomerID {
			out = append(out, cloneInvoice(i))
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out, nil
}

func (r invoiceRepo) AddPayment(ctx context.Context, p *domain.Payment) error {
	defer r.s.lock(ctx)()
	i, ok := r.s.invoices[p.InvoiceID]
	if !ok {
		return domain.NotFound("Factura no encontrada.")
	}
	p.ID = r.s.next("payment")
	i.Payments = append(slices.Clone(i.Payments), *p)
	r.s.invoices[i.ID] = i
	return nil
}

func (r invoiceRepo) ListPaymentsByTrip(ctx context.Context, tripID int64) ([]domain.Payment, error) {
	defer r.s.lock(ctx)()
	var out []domain.Payment
	for _, i := range r.s.invoices {
		for _, p := range i.Payments {
			if p.TripID == tripID {
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

// ---------- trips ----------

type tripRepo struct{ s *Store }

func cloneTrip(t domain.Trip) domain.Trip {
	t.Stops, t.Orders, t.Delivered = slices.Clone(t.Stops), slices.Clone(t.Orders), slices.Clone(t.Delivered)
	t.Sales, t.Presales = slices.Clone(t.Sales), slices.Clone(t.Presales)
	t.Load, t.Origin = slices.Clone(t.Load), slices.Clone(t.Origin)
	if t.Settlement != nil {
		s := *t.Settlement
		s.Returned = slices.Clone(s.Returned)
		t.Settlement = &s
	}
	return t
}

func (r tripRepo) Create(ctx context.Context, t *domain.Trip) error {
	defer r.s.lock(ctx)()
	t.ID = r.s.next("trip")
	r.s.trips[t.ID] = cloneTrip(*t)
	return nil
}

func (r tripRepo) Get(ctx context.Context, id int64) (domain.Trip, error) {
	defer r.s.lock(ctx)()
	t, ok := r.s.trips[id]
	if !ok {
		return domain.Trip{}, domain.NotFound("Viaje no encontrado.")
	}
	return cloneTrip(t), nil
}

func (r tripRepo) GetForUpdate(ctx context.Context, id int64) (domain.Trip, error) {
	return r.Get(ctx, id)
}

func (r tripRepo) FindOpen(ctx context.Context, truckID int64) (domain.Trip, bool, error) {
	defer r.s.lock(ctx)()
	for _, t := range r.s.trips {
		if t.TruckID == truckID && t.Status != domain.TripSettled {
			return cloneTrip(t), true, nil
		}
	}
	return domain.Trip{}, false, nil
}

func (r tripRepo) List(ctx context.Context, f usecase.TripFilter) ([]domain.Trip, error) {
	defer r.s.lock(ctx)()
	var out []domain.Trip
	for _, t := range r.s.trips {
		if (f.TruckID == 0 || t.TruckID == f.TruckID) && (f.Status == "" || t.Status == f.Status) {
			out = append(out, cloneTrip(t))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (r tripRepo) Save(ctx context.Context, t *domain.Trip) error {
	defer r.s.lock(ctx)()
	if _, ok := r.s.trips[t.ID]; !ok {
		return domain.NotFound("Viaje no encontrado.")
	}
	r.s.trips[t.ID] = cloneTrip(*t)
	return nil
}

// ---------- trucks ----------

type truckRepo struct{ s *Store }

func (r truckRepo) Create(ctx context.Context, t *domain.Truck) error {
	defer r.s.lock(ctx)()
	t.ID = r.s.next("truck")
	r.s.trucks[t.ID] = domain.Truck{ID: t.ID, Name: t.Name, Driver: t.Driver, Routes: slices.Clone(t.Routes)}
	return nil
}

func (r truckRepo) List(ctx context.Context) ([]domain.Truck, error) {
	defer r.s.lock(ctx)()
	out := make([]domain.Truck, 0, len(r.s.trucks))
	for _, t := range r.s.trucks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r truckRepo) Get(ctx context.Context, id int64) (domain.Truck, error) {
	defer r.s.lock(ctx)()
	t, ok := r.s.trucks[id]
	if !ok {
		return domain.Truck{}, domain.NotFound("Camión no encontrado.")
	}
	return t, nil
}

// ---------- exchange rate ----------

type rateRepo struct{ s *Store }

func (r rateRepo) Current(ctx context.Context) (domain.ExchangeRate, error) {
	defer r.s.lock(ctx)()
	if r.s.rate == nil {
		return domain.ExchangeRate{}, domain.NotFound("No hay tasa BCV registrada.")
	}
	return *r.s.rate, nil
}

func (r rateRepo) Set(ctx context.Context, er domain.ExchangeRate) error {
	defer r.s.lock(ctx)()
	r.s.rate = &er
	return nil
}

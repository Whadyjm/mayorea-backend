package domain

import (
	"slices"
	"time"
)

type TripStatus string

const (
	TripOnRoute  TripStatus = "en_ruta"
	TripToSettle TripStatus = "por_liquidar"
	TripSettled  TripStatus = "liquidado"
)

// Qty is a quantity of one product (a "renglón").
type Qty struct {
	ProductID int64
	Quantity  int
}

// SumQty merges quantities per product, dropping non-positive ones, keeping
// first-seen order.
func SumQty(groups ...[]Qty) []Qty {
	idx := map[int64]int{}
	var out []Qty
	for _, g := range groups {
		for _, q := range g {
			if q.Quantity <= 0 {
				continue
			}
			if i, ok := idx[q.ProductID]; ok {
				out[i].Quantity += q.Quantity
				continue
			}
			idx[q.ProductID] = len(out)
			out = append(out, q)
		}
	}
	return out
}

// LotTake records stock taken from a lot for the truck's extra load, so what
// comes back can return to the lot it left.
type LotTake struct {
	LotID     int64
	ProductID int64
	Quantity  int
	ExpiresOn time.Time
}

type Settlement struct {
	Date     time.Time
	CashUSD  Cents // cash the driver counted and hands over
	Returned []Qty // goods the driver counted back
	Note     string
}

// Trip is a truck on the street: what it carried, delivered, sold, took as
// presales and collected, until the office settles it.
type Trip struct {
	ID         int64
	TruckID    int64
	Driver     string
	Date       time.Time
	Status     TripStatus
	Stops      []int64 // customers in visiting order
	Orders     []int64 // invoiced orders that left to be delivered
	Delivered  []int64
	Sales      []int64 // sold on the street from truck stock
	Presales   []int64 // taken on the street for a future dispatch
	Load       []Qty   // everything that went up on the truck
	Origin     []LotTake
	Settlement *Settlement
}

func NewTrip(truck Truck, customers []Customer, orderIDs []int64, load []Qty, takes []LotTake, today time.Time) Trip {
	var stops []int64
	for _, c := range customers {
		if truck.Serves(c.Route) {
			stops = append(stops, c.ID)
		}
	}
	return Trip{
		TruckID: truck.ID, Driver: truck.Driver, Date: DateOf(today), Status: TripOnRoute,
		Stops: stops, Orders: slices.Clone(orderIDs), Load: SumQty(load), Origin: mergeTakes(nil, takes),
	}
}

func mergeTakes(a, b []LotTake) []LotTake {
	out := slices.Clone(a)
	for _, t := range b {
		i := slices.IndexFunc(out, func(x LotTake) bool { return x.LotID == t.LotID })
		if i >= 0 {
			out[i].Quantity += t.Quantity
		} else {
			out = append(out, t)
		}
	}
	return out
}

// AddLoad adds a second departure to a truck that is still out.
func (t *Trip) AddLoad(orderIDs []int64, load []Qty, takes []LotTake) {
	t.Orders = append(t.Orders, orderIDs...)
	t.Load = SumQty(t.Load, load)
	t.Origin = mergeTakes(t.Origin, takes)
}

func (t Trip) EnsureOnRoute() error {
	if t.Status != TripOnRoute {
		return Rule("La ruta ya se cerró.")
	}
	return nil
}

// OrderIDs lists every order tied to the trip.
func (t Trip) OrderIDs() []int64 {
	var ids []int64
	ids = append(ids, t.Orders...)
	ids = append(ids, t.Sales...)
	ids = append(ids, t.Presales...)
	return ids
}

func (t *Trip) Deliver(orderID int64) error {
	if err := t.EnsureOnRoute(); err != nil {
		return err
	}
	if !slices.Contains(t.Orders, orderID) {
		return Rule("Ese pedido no va en este camión.")
	}
	if slices.Contains(t.Delivered, orderID) {
		return Rule("Ese pedido ya se entregó.")
	}
	t.Delivered = append(t.Delivered, orderID)
	return nil
}

// Close ends the route and hands the driver's count to the office.
func (t *Trip) Close(s Settlement) error {
	if err := t.EnsureOnRoute(); err != nil {
		return err
	}
	if missing := len(t.Orders) - len(t.Delivered); missing == 1 {
		return Rule("Falta 1 entrega por marcar.")
	} else if missing > 1 {
		return Rule("Faltan %d entregas por marcar.", missing)
	}
	if s.CashUSD < 0 || slices.ContainsFunc(s.Returned, func(q Qty) bool { return q.Quantity < 0 }) {
		return Invalid("Revisa las cantidades.")
	}
	s.Returned = SumQty(s.Returned)
	t.Settlement = &s
	t.Status = TripToSettle
	return nil
}

// ---------- truck stock ----------

type TruckStock struct {
	ProductID int64
	Loaded    int
	ToDeliver int // from invoiced orders not yet delivered
	Sold      int
	Delivered int
	OnBoard   int // physically on the truck
	Free      int // sellable without touching other customers' orders
}

func linesOf(orders map[int64]Order, ids []int64) map[int64]int {
	m := map[int64]int{}
	for _, id := range ids {
		for _, l := range orders[id].Lines {
			m[l.ProductID] += l.Quantity
		}
	}
	return m
}

// Stock computes what is on the truck; orders must hold every order of the trip.
func (t Trip) Stock(orders map[int64]Order) []TruckStock {
	var pending []int64
	for _, id := range t.Orders {
		if !slices.Contains(t.Delivered, id) {
			pending = append(pending, id)
		}
	}
	toDeliver, sold, delivered := linesOf(orders, pending), linesOf(orders, t.Sales), linesOf(orders, t.Delivered)
	out := make([]TruckStock, 0, len(t.Load))
	for _, l := range t.Load {
		pe, ve, en := toDeliver[l.ProductID], sold[l.ProductID], delivered[l.ProductID]
		out = append(out, TruckStock{
			ProductID: l.ProductID, Loaded: l.Quantity, ToDeliver: pe, Sold: ve, Delivered: en,
			OnBoard: l.Quantity - en - ve, Free: l.Quantity - en - ve - pe,
		})
	}
	return out
}

// ---------- sales on the street ----------

// NewRouteSale builds an order sold from truck stock: billed and delivered on
// the spot (status dispatched). The caller persists the order, then bills it
// with NewInvoice.
func NewRouteSale(t Trip, stock []TruckStock, c Customer, credit CreditStatus, products map[int64]Product, terms Terms, lines []Qty, today time.Time) (Order, error) {
	if !terms.Valid() {
		return Order{}, Invalid("Condición de pago inválida.")
	}
	lines = SumQty(lines)
	if len(lines) == 0 {
		return Order{}, Invalid("Agrega al menos un producto.")
	}
	free := map[int64]int{}
	for _, s := range stock {
		free[s.ProductID] = s.Free
	}
	out := make([]OrderLine, 0, len(lines))
	for _, l := range lines {
		p, ok := products[l.ProductID]
		if !ok {
			return Order{}, NotFound("Producto no encontrado.")
		}
		if l.Quantity > free[l.ProductID] {
			return Order{}, Rule("En el camión solo quedan %d %ss libres de %s.", free[l.ProductID], p.Unit, p.Name)
		}
		out = append(out, OrderLine{ProductID: p.ID, Quantity: l.Quantity, PriceUSD: p.PriceUSD})
	}
	o := Order{
		CustomerID: c.ID, Date: DateOf(today), Salesperson: t.Driver, Channel: ChannelRoute,
		Terms: terms, Lines: out, Status: StatusDispatched, Note: "Venta en ruta",
	}
	if terms == TermsCredit {
		if credit.Blocked {
			return Order{}, Rule("%s tiene %d días de mora. Solo puede comprar de contado.", c.Name, credit.DaysOverdue)
		}
		if o.Total() > credit.AvailableUSD {
			return Order{}, Rule("Pasa el límite de crédito de %s. Cóbrala de contado o baja la cantidad.", c.Name)
		}
	}
	return o, nil
}

// ---------- closing and settlement ----------

type TripSummary struct {
	CashUSD      Cents
	Bs           Cents
	CollectedUSD Cents // everything collected, in USD
	Collections  []Payment
	SalesUSD     Cents
	PresalesUSD  Cents
	Delivered    int
	ToDeliver    int
	Return       []Qty // goods that should come back to the warehouse
}

func SummarizeTrip(t Trip, orders map[int64]Order, payments []Payment) TripSummary {
	s := TripSummary{
		Collections: payments, Delivered: len(t.Delivered), ToDeliver: len(t.Orders) - len(t.Delivered),
	}
	for _, p := range payments {
		s.CashUSD += p.CashUSD
		s.Bs += p.Bs
		s.CollectedUSD += p.AppliedUSD
	}
	for _, id := range t.Sales {
		s.SalesUSD += orders[id].Total()
	}
	for _, id := range t.Presales {
		s.PresalesUSD += orders[id].Total()
	}
	for _, st := range t.Stock(orders) {
		if st.OnBoard > 0 {
			s.Return = append(s.Return, Qty{st.ProductID, st.OnBoard})
		}
	}
	return s
}

type GoodsDiff struct {
	ProductID  int64
	Expected   int
	Returned   int
	Difference int
}

type Differences struct {
	CashUSD  Cents // declared minus expected; negative is a shortage
	Goods    []GoodsDiff
	Balanced bool
}

func CompareSettlement(sum TripSummary, s Settlement) Differences {
	expected := map[int64]int{}
	var ids []int64
	for _, q := range sum.Return {
		expected[q.ProductID] = q.Quantity
		ids = append(ids, q.ProductID)
	}
	returned := map[int64]int{}
	for _, q := range s.Returned {
		returned[q.ProductID] += q.Quantity
		if !slices.Contains(ids, q.ProductID) {
			ids = append(ids, q.ProductID)
		}
	}
	d := Differences{CashUSD: s.CashUSD - sum.CashUSD, Balanced: s.CashUSD == sum.CashUSD}
	for _, id := range ids {
		g := GoodsDiff{ProductID: id, Expected: expected[id], Returned: returned[id]}
		g.Difference = g.Returned - g.Expected
		if g.Difference != 0 {
			d.Balanced = false
		}
		d.Goods = append(d.Goods, g)
	}
	return d
}

// LotCredit puts quantity back into an existing lot.
type LotCredit struct {
	LotID    int64
	Quantity int
}

// PlanReturns decides where the goods the driver brought back go: into the
// earliest-expiring lot they left from, or a new "DEV" lot when their origin
// is unknown (they came from dispatched orders, which are not tracked per lot).
func (t Trip) PlanReturns(today time.Time) (credits []LotCredit, fresh []Lot) {
	if t.Settlement == nil {
		return nil, nil
	}
	for _, q := range t.Settlement.Returned {
		var best *LotTake
		for i := range t.Origin {
			o := &t.Origin[i]
			if o.ProductID != q.ProductID {
				continue
			}
			if best == nil || o.ExpiresOn.Before(best.ExpiresOn) || (o.ExpiresOn.Equal(best.ExpiresOn) && o.LotID < best.LotID) {
				best = o
			}
		}
		if best != nil {
			credits = append(credits, LotCredit{best.LotID, q.Quantity})
			continue
		}
		fresh = append(fresh, Lot{ProductID: q.ProductID, Code: "DEV", ExpiresOn: AddDays(today, 30), Quantity: q.Quantity, Location: "DEV"})
	}
	return credits, fresh
}

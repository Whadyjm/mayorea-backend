package domain

import "sort"

type Allocation struct {
	Lot      Lot
	Quantity int
}

// LotPool hands out stock first-expired-first-out (FEFO) and tracks which
// lots were touched so only those are written back.
type LotPool struct {
	byProduct map[int64][]*Lot
	touched   map[int64]*Lot
}

func NewLotPool(lots []Lot) *LotPool {
	p := &LotPool{byProduct: map[int64][]*Lot{}, touched: map[int64]*Lot{}}
	for _, l := range lots {
		if l.Quantity <= 0 {
			continue
		}
		l := l
		p.byProduct[l.ProductID] = append(p.byProduct[l.ProductID], &l)
	}
	for _, ls := range p.byProduct {
		sort.SliceStable(ls, func(i, j int) bool {
			if !ls[i].ExpiresOn.Equal(ls[j].ExpiresOn) {
				return ls[i].ExpiresOn.Before(ls[j].ExpiresOn)
			}
			return ls[i].ID < ls[j].ID
		})
	}
	return p
}

// Peek returns the FEFO allocation for qty without consuming anything, and
// the quantity that could not be covered.
func (p *LotPool) Peek(productID int64, qty int) ([]Allocation, int) {
	var out []Allocation
	for _, l := range p.byProduct[productID] {
		if qty <= 0 {
			break
		}
		if l.Quantity <= 0 {
			continue
		}
		n := min(qty, l.Quantity)
		out = append(out, Allocation{Lot: *l, Quantity: n})
		qty -= n
	}
	return out, qty
}

// Take consumes qty of a product by FEFO; it fails without side effects if
// the lots cannot cover it.
func (p *LotPool) Take(productID int64, qty int) ([]Allocation, error) {
	allocs, short := p.Peek(productID, qty)
	if short > 0 {
		return nil, Rule("No hay lotes suficientes en el galpón para despachar (faltan %d).", short)
	}
	for _, a := range allocs {
		for _, l := range p.byProduct[productID] {
			if l.ID == a.Lot.ID {
				l.Quantity -= a.Quantity
				p.touched[l.ID] = l
			}
		}
	}
	return allocs, nil
}

// Changed returns the lots whose quantity was reduced.
func (p *LotPool) Changed() []Lot {
	out := make([]Lot, 0, len(p.touched))
	for _, l := range p.touched {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// OrdersForTruck selects approved orders whose customer route the truck serves.
func OrdersForTruck(t Truck, orders []Order, customers map[int64]Customer) []Order {
	var out []Order
	for _, o := range orders {
		c, ok := customers[o.CustomerID]
		if o.Status == StatusApproved && ok && t.Serves(c.Route) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type LoadLine struct {
	ProductID   int64
	Quantity    int
	Allocations []Allocation
	Shortfall   int
}

// PlanLoad consolidates the orders into one picking list per truck, sorted by
// the aisle of the first lot to pick.
func PlanLoad(orders []Order, lots []Lot) []LoadLine {
	totals := map[int64]int{}
	for _, o := range orders {
		for pid, q := range o.QuantityByProduct() {
			totals[pid] += q
		}
	}
	pool := NewLotPool(lots)
	lines := make([]LoadLine, 0, len(totals))
	for pid, q := range totals {
		allocs, short := pool.Peek(pid, q)
		lines = append(lines, LoadLine{ProductID: pid, Quantity: q, Allocations: allocs, Shortfall: short})
	}
	loc := func(l LoadLine) string {
		if len(l.Allocations) == 0 {
			return ""
		}
		return l.Allocations[0].Lot.Location
	}
	sort.Slice(lines, func(i, j int) bool {
		if loc(lines[i]) != loc(lines[j]) {
			return loc(lines[i]) < loc(lines[j])
		}
		return lines[i].ProductID < lines[j].ProductID
	})
	return lines
}

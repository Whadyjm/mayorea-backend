package domain

import (
	"fmt"
	"time"
)

type Terms string

const (
	TermsCredit Terms = "credito"
	TermsCash   Terms = "contado"
)

func (t Terms) Valid() bool { return t == TermsCredit || t == TermsCash }

type Channel string

const (
	ChannelRoute    Channel = "ruta"
	ChannelWhatsApp Channel = "whatsapp"
	ChannelOffice   Channel = "oficina"
)

func (c Channel) Valid() bool {
	return c == ChannelRoute || c == ChannelWhatsApp || c == ChannelOffice
}

type OrderStatus string

const (
	StatusPending    OrderStatus = "por_aprobar"
	StatusApproved   OrderStatus = "aprobado"
	StatusDispatched OrderStatus = "despachado"
	StatusRejected   OrderStatus = "rechazado"
)

// IsOpen: the order still holds reserved stock and counts against credit.
func (s OrderStatus) IsOpen() bool { return s == StatusPending || s == StatusApproved }

type OrderLine struct {
	ProductID int64
	Quantity  int
	PriceUSD  Cents // price frozen when the order was taken
}

type Order struct {
	ID          int64
	Number      int64
	CustomerID  int64
	Date        time.Time
	Salesperson string
	Channel     Channel
	Terms       Terms
	Lines       []OrderLine
	Status      OrderStatus
	Note        string
}

func (o Order) Total() Cents {
	var t Cents
	for _, l := range o.Lines {
		t += l.PriceUSD * Cents(l.Quantity)
	}
	return t
}

// QuantityByProduct totals the order's lines per product.
func (o Order) QuantityByProduct() map[int64]int {
	m := map[int64]int{}
	for _, l := range o.Lines {
		m[l.ProductID] += l.Quantity
	}
	return m
}

type DraftLine struct {
	ProductID int64
	Quantity  int
}

type OrderDraft struct {
	CustomerID  int64
	Terms       Terms
	Channel     Channel
	Salesperson string
	Note        string
	Lines       []DraftLine
}

// NewOrder validates a draft against the customer's credit and the stock
// available, and returns the order to persist (status pending approval).
// The caller must then reserve the stock via QuantityByProduct.
func NewOrder(d OrderDraft, c Customer, credit CreditStatus, products map[int64]Product, today time.Time) (Order, error) {
	if !d.Terms.Valid() {
		return Order{}, Invalid("Condición de pago inválida.")
	}
	if !d.Channel.Valid() {
		return Order{}, Invalid("Canal de pedido inválido.")
	}
	lines := mergeLines(d.Lines)
	if len(lines) == 0 {
		return Order{}, Invalid("Agrega al menos un producto.")
	}
	if d.Terms == TermsCredit && credit.Blocked {
		return Order{}, Rule("%s tiene %d días de mora. Solo puede comprar de contado hasta que pague.", c.Name, credit.DaysOverdue)
	}

	out := make([]OrderLine, 0, len(lines))
	for _, l := range lines {
		p, ok := products[l.ProductID]
		if !ok {
			return Order{}, NotFound("Producto no encontrado.")
		}
		if free := p.Available(); l.Quantity > free {
			return Order{}, Rule("Solo quedan %d %ss disponibles de %s.", max(free, 0), p.Unit, p.Name)
		}
		out = append(out, OrderLine{ProductID: p.ID, Quantity: l.Quantity, PriceUSD: p.PriceUSD})
	}
	return Order{
		CustomerID: c.ID, Date: DateOf(today), Salesperson: d.Salesperson,
		Channel: d.Channel, Terms: d.Terms, Lines: out, Status: StatusPending, Note: d.Note,
	}, nil
}

// mergeLines drops empty lines and sums repeated products, keeping order.
func mergeLines(in []DraftLine) []DraftLine {
	idx := map[int64]int{}
	var out []DraftLine
	for _, l := range in {
		if l.Quantity <= 0 {
			continue
		}
		if i, ok := idx[l.ProductID]; ok {
			out[i].Quantity += l.Quantity
			continue
		}
		idx[l.ProductID] = len(out)
		out = append(out, l)
	}
	return out
}

type CreditAlertKind string

const (
	AlertOverdue CreditAlertKind = "mora"
	AlertLimit   CreditAlertKind = "limite"
	AlertOK      CreditAlertKind = "ok"
	AlertCash    CreditAlertKind = "contado"
)

type CreditAlert struct {
	Kind CreditAlertKind
	Text string
}

// ReviewOrder is the signal the credit officer sees before approving.
func ReviewOrder(o Order, c Customer, st CreditStatus) CreditAlert {
	switch {
	case o.Terms == TermsCash:
		return CreditAlert{AlertCash, "De contado"}
	case st.Blocked:
		return CreditAlert{AlertOverdue, fmt.Sprintf("Mora %d días", st.DaysOverdue)}
	case st.ExceedsLimit(c) && c.CreditLimitUSD > 0:
		used := st.BalanceUSD + st.OpenOrdersUSD
		return CreditAlert{AlertLimit, fmt.Sprintf("Pasa el límite (%d%%)", int64(used)*100/int64(c.CreditLimitUSD))}
	case st.ExceedsLimit(c):
		return CreditAlert{AlertLimit, "Pasa el límite"}
	}
	return CreditAlert{AlertOK, "Crédito al día"}
}

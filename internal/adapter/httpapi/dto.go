package httpapi

import (
	"time"

	"github.com/mayorea/backend/internal/domain"
)

// DTOs are the public JSON contract. Money is integer cents, rates are
// ten-thousandths of Bs/USD, dates are "YYYY-MM-DD".

const dateLayout = "2006-01-02"

func fmtDate(t time.Time) string { return t.Format(dateLayout) }

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, domain.Invalid("Fecha inválida, usa AAAA-MM-DD.")
	}
	return t, nil
}

type productDTO struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	Unit      domain.Unit  `json:"unit"`
	PriceUSD  domain.Cents `json:"priceUsd"`
	Committed int          `json:"committed"`
	MinStock  int          `json:"minStock"`
	Physical  int          `json:"physical"`
	Available int          `json:"available"`
}

func toProduct(p domain.Product) productDTO {
	return productDTO{p.ID, p.Name, p.Unit, p.PriceUSD, p.Committed, p.MinStock, p.Physical, p.Available()}
}

type lotDTO struct {
	ID        int64  `json:"id"`
	ProductID int64  `json:"productId"`
	Code      string `json:"code"`
	ExpiresOn string `json:"expiresOn"`
	Quantity  int    `json:"quantity"`
	Location  string `json:"location"`
}

func toLot(l domain.Lot) lotDTO {
	return lotDTO{l.ID, l.ProductID, l.Code, fmtDate(l.ExpiresOn), l.Quantity, l.Location}
}

type customerDTO struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	RIF            string       `json:"rif"`
	Route          string       `json:"route"`
	Phone          string       `json:"phone"`
	CreditLimitUSD domain.Cents `json:"creditLimitUsd"`
	CreditDays     int          `json:"creditDays"`
}

func toCustomer(c domain.Customer) customerDTO {
	return customerDTO{c.ID, c.Name, c.RIF, c.Route, c.Phone, c.CreditLimitUSD, c.CreditDays}
}

type creditDTO struct {
	BalanceUSD    domain.Cents `json:"balanceUsd"`
	OverdueUSD    domain.Cents `json:"overdueUsd"`
	DaysOverdue   int          `json:"daysOverdue"`
	AvailableUSD  domain.Cents `json:"availableUsd"`
	OpenOrdersUSD domain.Cents `json:"openOrdersUsd"`
	Blocked       bool         `json:"blocked"`
}

func toCredit(s domain.CreditStatus) creditDTO {
	return creditDTO{s.BalanceUSD, s.OverdueUSD, s.DaysOverdue, s.AvailableUSD, s.OpenOrdersUSD, s.Blocked}
}

type truckDTO struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Driver string   `json:"driver"`
	Routes []string `json:"routes"`
}

func toTruck(t domain.Truck) truckDTO {
	routes := t.Routes
	if routes == nil {
		routes = []string{}
	}
	return truckDTO{t.ID, t.Name, t.Driver, routes}
}

type orderLineDTO struct {
	ProductID int64        `json:"productId"`
	Quantity  int          `json:"quantity"`
	PriceUSD  domain.Cents `json:"priceUsd"`
}

type orderDTO struct {
	ID          int64              `json:"id"`
	Number      int64              `json:"number"`
	CustomerID  int64              `json:"customerId"`
	Date        string             `json:"date"`
	Salesperson string             `json:"salesperson"`
	Channel     domain.Channel     `json:"channel"`
	Terms       domain.Terms       `json:"terms"`
	Status      domain.OrderStatus `json:"status"`
	Note        string             `json:"note"`
	TotalUSD    domain.Cents       `json:"totalUsd"`
	Lines       []orderLineDTO     `json:"lines"`
}

func toOrder(o domain.Order) orderDTO {
	lines := make([]orderLineDTO, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = orderLineDTO{l.ProductID, l.Quantity, l.PriceUSD}
	}
	return orderDTO{o.ID, o.Number, o.CustomerID, fmtDate(o.Date), o.Salesperson, o.Channel, o.Terms, o.Status, o.Note, o.Total(), lines}
}

type paymentDTO struct {
	ID             int64        `json:"id"`
	InvoiceID      int64        `json:"invoiceId"`
	Date           string       `json:"date"`
	CashUSD        domain.Cents `json:"cashUsd"`
	Bs             domain.Cents `json:"bs"`
	Rate           domain.Rate  `json:"rate"`
	Reference      string       `json:"reference"`
	AppliedUSD     domain.Cents `json:"appliedUsd"`
	ExchangeDiffBs domain.Cents `json:"exchangeDiffBs"`
	TripID         int64        `json:"tripId,omitempty"` // set for payments collected on the street
}

func toPayment(p domain.Payment) paymentDTO {
	return paymentDTO{p.ID, p.InvoiceID, fmtDate(p.Date), p.CashUSD, p.Bs, p.Rate, p.Reference, p.AppliedUSD, p.ExchangeDiffBs, p.TripID}
}

type invoiceDTO struct {
	ID         int64        `json:"id"`
	Number     int64        `json:"number"`
	OrderID    int64        `json:"orderId"`
	CustomerID int64        `json:"customerId"`
	IssuedOn   string       `json:"issuedOn"`
	DueOn      string       `json:"dueOn"`
	TotalUSD   domain.Cents `json:"totalUsd"`
	IssueRate  domain.Rate  `json:"issueRate"`
	PaidUSD    domain.Cents `json:"paidUsd"`
	BalanceUSD domain.Cents `json:"balanceUsd"`
	Payments   []paymentDTO `json:"payments"`
}

func toInvoice(i domain.Invoice) invoiceDTO {
	pays := make([]paymentDTO, len(i.Payments))
	for k, p := range i.Payments {
		pays[k] = toPayment(p)
	}
	return invoiceDTO{i.ID, i.Number, i.OrderID, i.CustomerID, fmtDate(i.IssuedOn), fmtDate(i.DueOn),
		i.TotalUSD, i.IssueRate, i.Paid(), i.Balance(), pays}
}

func mapList[T, R any](in []T, f func(T) R) []R {
	out := make([]R, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

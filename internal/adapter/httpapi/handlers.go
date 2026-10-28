package httpapi

import (
	"net/http"
	"strings"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

// Services groups the use cases the API exposes.
type Services struct {
	Catalog     *usecase.CatalogService
	Orders      *usecase.OrderService
	Dispatch    *usecase.DispatchService
	Collections *usecase.CollectionService
	Trips       *usecase.TripService
	Auth        *usecase.AuthService
}

type handlers struct{ Services }

// ---------- exchange rate ----------

type rateDTO struct {
	Rate      domain.Rate `json:"rate"`
	UpdatedOn string      `json:"updatedOn"`
}

func (h handlers) getRate(w http.ResponseWriter, r *http.Request) {
	er, err := h.Catalog.CurrentRate(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rateDTO{er.Rate, fmtDate(er.UpdatedOn)})
}

func (h handlers) setRate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rate domain.Rate `json:"rate"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	er, err := h.Catalog.SetRate(r.Context(), in.Rate)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rateDTO{er.Rate, fmtDate(er.UpdatedOn)})
}

// ---------- products & lots ----------

func (h handlers) listProducts(w http.ResponseWriter, r *http.Request) {
	ps, err := h.Catalog.ListProducts(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(ps, toProduct))
}

func (h handlers) createProduct(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string       `json:"name"`
		Unit     domain.Unit  `json:"unit"`
		PriceUSD domain.Cents `json:"priceUsd"`
		MinStock int          `json:"minStock"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Catalog.CreateProduct(r.Context(), usecase.NewProduct{Name: in.Name, Unit: in.Unit, PriceUSD: in.PriceUSD, MinStock: in.MinStock})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toProduct(p))
}

func (h handlers) listLots(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	ls, err := h.Catalog.ListLots(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(ls, toLot))
}

func (h handlers) receiveLot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Code      string `json:"code"`
		ExpiresOn string `json:"expiresOn"`
		Quantity  int    `json:"quantity"`
		Location  string `json:"location"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	exp, err := parseDate(in.ExpiresOn)
	if err != nil {
		writeError(w, r, err)
		return
	}
	l, err := h.Catalog.ReceiveLot(r.Context(), usecase.NewLot{ProductID: id, Code: in.Code, ExpiresOn: exp, Quantity: in.Quantity, Location: in.Location})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toLot(l))
}

// ---------- customers ----------

func (h handlers) listCustomers(w http.ResponseWriter, r *http.Request) {
	cs, err := h.Catalog.ListCustomers(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(cs, toCustomer))
}

func (h handlers) createCustomer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string       `json:"name"`
		RIF            string       `json:"rif"`
		Route          string       `json:"route"`
		Phone          string       `json:"phone"`
		CreditLimitUSD domain.Cents `json:"creditLimitUsd"`
		CreditDays     int          `json:"creditDays"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	c, err := h.Catalog.CreateCustomer(r.Context(), usecase.NewCustomer{
		Name: in.Name, RIF: in.RIF, Route: in.Route, Phone: in.Phone, CreditLimitUSD: in.CreditLimitUSD, CreditDays: in.CreditDays,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toCustomer(c))
}

func (h handlers) customerCredit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	st, err := h.Orders.CustomerCredit(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toCredit(st))
}

// ---------- orders ----------

func (h handlers) listOrders(w http.ResponseWriter, r *http.Request) {
	cid, err := queryInt(r, "customerId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	f := usecase.OrderFilter{CustomerID: cid}
	if s := r.URL.Query().Get("status"); s != "" {
		f.Statuses = []domain.OrderStatus{domain.OrderStatus(s)}
	}
	os, err := h.Orders.List(r.Context(), f)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(os, toOrder))
}

func (h handlers) createOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CustomerID  int64          `json:"customerId"`
		Terms       domain.Terms   `json:"terms"`
		Channel     domain.Channel `json:"channel"`
		Salesperson string         `json:"salesperson"`
		Note        string         `json:"note"`
		Lines       []struct {
			ProductID int64 `json:"productId"`
			Quantity  int   `json:"quantity"`
		} `json:"lines"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	// A salesperson's orders are always attributed to them; office staff may
	// enter an order on someone else's behalf.
	p, _ := principalFrom(r.Context())
	if p.Role == domain.RoleSalesperson || strings.TrimSpace(in.Salesperson) == "" {
		in.Salesperson = p.Name
	}
	draft := domain.OrderDraft{
		CustomerID: in.CustomerID, Terms: in.Terms, Channel: in.Channel, Salesperson: in.Salesperson, Note: in.Note,
	}
	for _, l := range in.Lines {
		draft.Lines = append(draft.Lines, domain.DraftLine{ProductID: l.ProductID, Quantity: l.Quantity})
	}
	o, err := h.Orders.Create(r.Context(), draft)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toOrder(o))
}

func (h handlers) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	o, err := h.Orders.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrder(o))
}

func (h handlers) reviewOrder(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	rev, err := h.Orders.Review(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"alert":  map[string]any{"kind": rev.Alert.Kind, "text": rev.Alert.Text},
		"credit": toCredit(rev.Credit),
	})
}

func (h handlers) transition(f func(*usecase.OrderService, *http.Request, int64) (domain.Order, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeError(w, r, err)
			return
		}
		o, err := f(h.Orders, r, id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, toOrder(o))
	}
}

// ---------- trucks & dispatch ----------

func (h handlers) listTrucks(w http.ResponseWriter, r *http.Request) {
	ts, err := h.Catalog.ListTrucks(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(ts, toTruck))
}

func (h handlers) createTruck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string   `json:"name"`
		Driver string   `json:"driver"`
		Routes []string `json:"routes"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.Catalog.CreateTruck(r.Context(), usecase.NewTruck{Name: in.Name, Driver: in.Driver, Routes: in.Routes})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTruck(t))
}

type allocationDTO struct {
	LotID     int64  `json:"lotId"`
	LotCode   string `json:"lotCode"`
	ExpiresOn string `json:"expiresOn"`
	Location  string `json:"location"`
	Quantity  int    `json:"quantity"`
}

type loadLineDTO struct {
	ProductID   int64           `json:"productId"`
	Quantity    int             `json:"quantity"`
	Shortfall   int             `json:"shortfall"`
	Allocations []allocationDTO `json:"allocations"`
}

func (h handlers) loadPlan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	plan, err := h.Dispatch.Plan(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	lines := mapList(plan.Lines, func(l domain.LoadLine) loadLineDTO {
		return loadLineDTO{l.ProductID, l.Quantity, l.Shortfall, mapList(l.Allocations, func(a domain.Allocation) allocationDTO {
			return allocationDTO{a.Lot.ID, a.Lot.Code, fmtDate(a.Lot.ExpiresOn), a.Lot.Location, a.Quantity}
		})}
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"truck":  toTruck(plan.Truck),
		"orders": mapList(plan.Orders, toOrder),
		"lines":  lines,
	})
}

func (h handlers) dispatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	invs, err := h.Dispatch.Dispatch(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": mapList(invs, toInvoice)})
}

// ---------- invoices & payments ----------

func (h handlers) listInvoices(w http.ResponseWriter, r *http.Request) {
	cid, err := queryInt(r, "customerId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	is, err := h.Collections.ListInvoices(r.Context(), cid, r.URL.Query().Get("pending") == "true")
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(is, toInvoice))
}

func (h handlers) getInvoice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	i, err := h.Collections.GetInvoice(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toInvoice(i))
}

type paymentInput struct {
	CashUSD   domain.Cents `json:"cashUsd"`
	Bs        domain.Cents `json:"bs"`
	Reference string       `json:"reference"`
}

func (h handlers) previewPayment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in paymentInput
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	calc, err := h.Collections.PreviewPayment(r.Context(), id, in.CashUSD, in.Bs)
	body := map[string]any{
		"balanceBefore":      calc.BalanceBefore,
		"remainingAfterCash": calc.RemainingAfterCash,
		"suggestedBs":        calc.SuggestedBs,
		"usdFromBs":          calc.UsdFromBs,
		"balanceAfter":       calc.BalanceAfter,
		"exchangeDiffBs":     calc.ExchangeDiffBs,
	}
	// A rule violation is part of the preview, not a failed request.
	if domain.KindOf(err) == domain.KindRule {
		body["error"] = err.Error()
	} else if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h handlers) registerPayment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in paymentInput
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Collections.RegisterPayment(r.Context(), id, usecase.NewPayment{CashUSD: in.CashUSD, Bs: in.Bs, Reference: in.Reference})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPayment(p))
}

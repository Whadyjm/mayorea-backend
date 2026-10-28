package httpapi

import (
	"net/http"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

type qtyDTO struct {
	ProductID int64 `json:"productId"`
	Quantity  int   `json:"quantity"`
}

func toQtys(in []qtyDTO) []domain.Qty {
	return mapList(in, func(q qtyDTO) domain.Qty { return domain.Qty{ProductID: q.ProductID, Quantity: q.Quantity} })
}

func fromQtys(in []domain.Qty) []qtyDTO {
	return mapList(in, func(q domain.Qty) qtyDTO { return qtyDTO{q.ProductID, q.Quantity} })
}

type tripDTO struct {
	ID         int64             `json:"id"`
	TruckID    int64             `json:"truckId"`
	Driver     string            `json:"driver"`
	Date       string            `json:"date"`
	Status     domain.TripStatus `json:"status"`
	Stops      []int64           `json:"stops"`
	Orders     []int64           `json:"orders"`
	Delivered  []int64           `json:"delivered"`
	Sales      []int64           `json:"sales"`
	Presales   []int64           `json:"presales"`
	Load       []qtyDTO          `json:"load"`
	Settlement *settlementDTO    `json:"settlement"`
}

type settlementDTO struct {
	Date     string       `json:"date"`
	CashUSD  domain.Cents `json:"cashUsd"`
	Returned []qtyDTO     `json:"returned"`
	Note     string       `json:"note"`
}

func ids(in []int64) []int64 {
	if in == nil {
		return []int64{}
	}
	return in
}

func toTrip(t domain.Trip) tripDTO {
	d := tripDTO{
		ID: t.ID, TruckID: t.TruckID, Driver: t.Driver, Date: fmtDate(t.Date), Status: t.Status,
		Stops: ids(t.Stops), Orders: ids(t.Orders), Delivered: ids(t.Delivered), Sales: ids(t.Sales), Presales: ids(t.Presales),
		Load: fromQtys(t.Load),
	}
	if s := t.Settlement; s != nil {
		d.Settlement = &settlementDTO{fmtDate(s.Date), s.CashUSD, fromQtys(s.Returned), s.Note}
	}
	return d
}

type truckStockDTO struct {
	ProductID int64 `json:"productId"`
	Loaded    int   `json:"loaded"`
	ToDeliver int   `json:"toDeliver"`
	Sold      int   `json:"sold"`
	Delivered int   `json:"delivered"`
	OnBoard   int   `json:"onBoard"`
	Free      int   `json:"free"`
}

type tripDetailDTO struct {
	Trip        tripDTO         `json:"trip"`
	Orders      []orderDTO      `json:"orders"`
	Stock       []truckStockDTO `json:"stock"`
	Summary     summaryDTO      `json:"summary"`
	Differences *differencesDTO `json:"differences"`
}

type summaryDTO struct {
	CashUSD      domain.Cents `json:"cashUsd"`
	Bs           domain.Cents `json:"bs"`
	CollectedUSD domain.Cents `json:"collectedUsd"`
	Collections  []paymentDTO `json:"collections"`
	SalesUSD     domain.Cents `json:"salesUsd"`
	PresalesUSD  domain.Cents `json:"presalesUsd"`
	Delivered    int          `json:"delivered"`
	ToDeliver    int          `json:"toDeliver"`
	Return       []qtyDTO     `json:"return"`
}

type differencesDTO struct {
	CashUSD  domain.Cents `json:"cashUsd"`
	Balanced bool         `json:"balanced"`
	Goods    []struct {
		ProductID  int64 `json:"productId"`
		Expected   int   `json:"expected"`
		Returned   int   `json:"returned"`
		Difference int   `json:"difference"`
	} `json:"goods"`
}

func toTripDetail(d usecase.TripDetail) tripDetailDTO {
	out := tripDetailDTO{
		Trip:   toTrip(d.Trip),
		Orders: mapList(d.Orders, toOrder),
		Stock: mapList(d.Stock, func(s domain.TruckStock) truckStockDTO {
			return truckStockDTO{s.ProductID, s.Loaded, s.ToDeliver, s.Sold, s.Delivered, s.OnBoard, s.Free}
		}),
		Summary: summaryDTO{
			CashUSD: d.Summary.CashUSD, Bs: d.Summary.Bs, CollectedUSD: d.Summary.CollectedUSD,
			Collections: mapList(d.Summary.Collections, toPayment), SalesUSD: d.Summary.SalesUSD,
			PresalesUSD: d.Summary.PresalesUSD, Delivered: d.Summary.Delivered, ToDeliver: d.Summary.ToDeliver,
			Return: fromQtys(d.Summary.Return),
		},
	}
	if df := d.Differences; df != nil {
		dto := &differencesDTO{CashUSD: df.CashUSD, Balanced: df.Balanced}
		for _, g := range df.Goods {
			dto.Goods = append(dto.Goods, struct {
				ProductID  int64 `json:"productId"`
				Expected   int   `json:"expected"`
				Returned   int   `json:"returned"`
				Difference int   `json:"difference"`
			}{g.ProductID, g.Expected, g.Returned, g.Difference})
		}
		out.Differences = dto
	}
	return out
}

func (h handlers) depart(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Extra []qtyDTO `json:"extra"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			writeError(w, r, err)
			return
		}
	}
	t, err := h.Trips.Depart(r.Context(), id, toQtys(in.Extra))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTrip(t))
}

func (h handlers) listTrips(w http.ResponseWriter, r *http.Request) {
	tid, err := queryInt(r, "truckId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	// A driver only ever sees their own truck's trips.
	if p, _ := principalFrom(r.Context()); p.Role == domain.RoleDriver {
		if p.TruckID == 0 {
			writeError(w, r, domain.Forbidden("No tienes un camión asignado."))
			return
		}
		tid = p.TruckID
	}
	ts, err := h.Trips.List(r.Context(), usecase.TripFilter{TruckID: tid, Status: domain.TripStatus(r.URL.Query().Get("status"))})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(ts, toTrip))
}

func (h handlers) getTrip(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	d, err := h.Trips.Detail(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTripDetail(d))
}

func (h handlers) deliver(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		OrderID int64 `json:"orderId"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.Trips.Deliver(r.Context(), id, in.OrderID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTrip(t))
}

type tripOrderInput struct {
	CustomerID int64        `json:"customerId"`
	Terms      domain.Terms `json:"terms"`
	Lines      []qtyDTO     `json:"lines"`
	Note       string       `json:"note"`
}

func (h handlers) routeSale(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in tripOrderInput
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	o, inv, err := h.Trips.Sell(r.Context(), id, usecase.RouteSale{CustomerID: in.CustomerID, Terms: in.Terms, Lines: toQtys(in.Lines)})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": toOrder(o), "invoice": toInvoice(inv)})
}

func (h handlers) presale(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in tripOrderInput
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	draft := domain.OrderDraft{CustomerID: in.CustomerID, Terms: in.Terms, Note: in.Note}
	for _, l := range in.Lines {
		draft.Lines = append(draft.Lines, domain.DraftLine{ProductID: l.ProductID, Quantity: l.Quantity})
	}
	o, err := h.Trips.Presale(r.Context(), id, draft)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toOrder(o))
}

func (h handlers) routeCollect(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		InvoiceID int64 `json:"invoiceId"`
		paymentInput
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.Trips.Collect(r.Context(), id, in.InvoiceID, usecase.NewPayment{CashUSD: in.CashUSD, Bs: in.Bs, Reference: in.Reference})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPayment(p))
}

func (h handlers) closeTrip(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		CashUSD  domain.Cents `json:"cashUsd"`
		Returned []qtyDTO     `json:"returned"`
		Note     string       `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	d, err := h.Trips.Close(r.Context(), id, usecase.CloseInput{CashUSD: in.CashUSD, Returned: toQtys(in.Returned), Note: in.Note})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTripDetail(d))
}

func (h handlers) approveSettlement(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.Trips.ApproveSettlement(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTrip(t))
}

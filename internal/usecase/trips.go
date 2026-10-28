package usecase

import (
	"context"

	"github.com/mayorea/backend/internal/domain"
)

// TripService runs a truck's day on the street: departure, deliveries, sales
// from truck stock, presales, collections, route closing and the office's
// settlement. Every operation locks the trip row, so a driver's phone and the
// office cannot step on each other.
type TripService struct {
	tx        TxManager
	trips     TripRepository
	trucks    TruckRepository
	orders    OrderRepository
	customers CustomerRepository
	products  ProductRepository
	invoices  InvoiceRepository
	lots      LotRepository
	rates     RateRepository
	dispatch  *DispatchService
	ordersSvc *OrderService
	collect   *CollectionService
	clock     Clock
}

func NewTripService(tx TxManager, trips TripRepository, trucks TruckRepository, orders OrderRepository,
	customers CustomerRepository, products ProductRepository, invoices InvoiceRepository, lots LotRepository,
	rates RateRepository, dispatch *DispatchService, ordersSvc *OrderService, collect *CollectionService, clock Clock) *TripService {
	return &TripService{tx: tx, trips: trips, trucks: trucks, orders: orders, customers: customers, products: products,
		invoices: invoices, lots: lots, rates: rates, dispatch: dispatch, ordersSvc: ordersSvc, collect: collect, clock: clock}
}

// Depart bills and loads the truck's approved orders plus an extra load to
// sell on the street. If the truck is still out, the load is added to its trip.
func (s *TripService) Depart(ctx context.Context, truckID int64, extra []domain.Qty) (domain.Trip, error) {
	var out domain.Trip
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		truck, err := s.trucks.Get(ctx, truckID)
		if err != nil {
			return err
		}
		open, found, err := s.trips.FindOpen(ctx, truckID)
		if err != nil {
			return err
		}
		if found && open.Status != domain.TripOnRoute {
			return domain.Rule("%s está esperando su liquidación.", truck.Name)
		}
		res, err := s.dispatch.Ship(ctx, truck, extra)
		if err != nil {
			return err
		}

		ids := make([]int64, len(res.Orders))
		var load []domain.Qty
		for i, o := range res.Orders {
			ids[i] = o.ID
			for _, l := range o.Lines {
				load = append(load, domain.Qty{ProductID: l.ProductID, Quantity: l.Quantity})
			}
		}
		load = append(load, extra...)

		if found {
			open.AddLoad(ids, load, res.Takes)
			if err := s.trips.Save(ctx, &open); err != nil {
				return err
			}
			out = open
			return nil
		}
		customers, err := s.customers.List(ctx)
		if err != nil {
			return err
		}
		trip := domain.NewTrip(truck, customers, ids, load, res.Takes, s.clock.Today())
		if err := s.trips.Create(ctx, &trip); err != nil {
			return err
		}
		out = trip
		return nil
	})
	return out, err
}

// Get returns the trip alone (cheap; used for authorization checks).
func (s *TripService) Get(ctx context.Context, id int64) (domain.Trip, error) {
	return s.trips.Get(ctx, id)
}

func (s *TripService) List(ctx context.Context, f TripFilter) ([]domain.Trip, error) {
	return s.trips.List(ctx, f)
}

type TripDetail struct {
	Trip        domain.Trip
	Orders      []domain.Order
	Stock       []domain.TruckStock
	Summary     domain.TripSummary
	Differences *domain.Differences // present once the route is closed
}

func (s *TripService) Detail(ctx context.Context, id int64) (TripDetail, error) {
	trip, err := s.trips.Get(ctx, id)
	if err != nil {
		return TripDetail{}, err
	}
	return s.detail(ctx, trip)
}

func (s *TripService) detail(ctx context.Context, trip domain.Trip) (TripDetail, error) {
	orders, err := s.orders.FindByIDs(ctx, trip.OrderIDs())
	if err != nil {
		return TripDetail{}, err
	}
	pays, err := s.invoices.ListPaymentsByTrip(ctx, trip.ID)
	if err != nil {
		return TripDetail{}, err
	}
	d := TripDetail{Trip: trip, Stock: trip.Stock(orders), Summary: domain.SummarizeTrip(trip, orders, pays)}
	for _, id := range trip.OrderIDs() {
		if o, ok := orders[id]; ok {
			d.Orders = append(d.Orders, o)
		}
	}
	if trip.Settlement != nil {
		diff := domain.CompareSettlement(d.Summary, *trip.Settlement)
		d.Differences = &diff
	}
	return d, nil
}

// open locks the trip and checks the driver can still operate on it.
func (s *TripService) open(ctx context.Context, id int64) (domain.Trip, error) {
	trip, err := s.trips.GetForUpdate(ctx, id)
	if err != nil {
		return trip, err
	}
	return trip, trip.EnsureOnRoute()
}

func (s *TripService) Deliver(ctx context.Context, tripID, orderID int64) (domain.Trip, error) {
	var out domain.Trip
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		trip, err := s.open(ctx, tripID)
		if err != nil {
			return err
		}
		if err := trip.Deliver(orderID); err != nil {
			return err
		}
		out = trip
		return s.trips.Save(ctx, &trip)
	})
	return out, err
}

type RouteSale struct {
	CustomerID int64
	Terms      domain.Terms
	Lines      []domain.Qty
}

// Sell sells truck stock on the spot: the order is billed and delivered at once.
func (s *TripService) Sell(ctx context.Context, tripID int64, in RouteSale) (domain.Order, domain.Invoice, error) {
	var (
		order domain.Order
		inv   domain.Invoice
	)
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		trip, err := s.open(ctx, tripID)
		if err != nil {
			return err
		}
		c, err := s.customers.Get(ctx, in.CustomerID)
		if err != nil {
			return err
		}
		credit, err := s.ordersSvc.credit(ctx, c)
		if err != nil {
			return err
		}
		tripOrders, err := s.orders.FindByIDs(ctx, trip.OrderIDs())
		if err != nil {
			return err
		}
		lines := domain.SumQty(in.Lines)
		ids := make([]int64, len(lines))
		for i, l := range lines {
			ids[i] = l.ProductID
		}
		products, err := s.products.FindByIDs(ctx, ids)
		if err != nil {
			return err
		}
		today := s.clock.Today()
		order, err = domain.NewRouteSale(trip, trip.Stock(tripOrders), c, credit, products, in.Terms, lines, today)
		if err != nil {
			return err
		}
		rate, err := s.rates.Current(ctx)
		if err != nil {
			return domain.Rule("Registra la tasa BCV del día antes de vender.")
		}
		if err := s.orders.Create(ctx, &order); err != nil {
			return err
		}
		inv = domain.NewInvoice(order, c, rate.Rate, today)
		if err := s.invoices.Create(ctx, &inv); err != nil {
			return err
		}
		trip.Sales = append(trip.Sales, order.ID)
		return s.trips.Save(ctx, &trip)
	})
	return order, inv, err
}

// Presale takes an order on the street for a future dispatch: it reserves
// warehouse stock and waits for credit approval like any other order.
func (s *TripService) Presale(ctx context.Context, tripID int64, draft domain.OrderDraft) (domain.Order, error) {
	var out domain.Order
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		trip, err := s.open(ctx, tripID)
		if err != nil {
			return err
		}
		draft.Channel, draft.Salesperson = domain.ChannelRoute, trip.Driver
		out, err = s.ordersSvc.Create(ctx, draft)
		if err != nil {
			return err
		}
		trip.Presales = append(trip.Presales, out.ID)
		return s.trips.Save(ctx, &trip)
	})
	return out, err
}

// Collect registers a payment received on the street; it counts in the trip's settlement.
func (s *TripService) Collect(ctx context.Context, tripID, invoiceID int64, in NewPayment) (domain.Payment, error) {
	var out domain.Payment
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		if _, err := s.open(ctx, tripID); err != nil {
			return err
		}
		in.TripID = tripID
		p, err := s.collect.RegisterPayment(ctx, invoiceID, in)
		out = p
		return err
	})
	return out, err
}

type CloseInput struct {
	CashUSD  domain.Cents
	Returned []domain.Qty
	Note     string
}

// Close ends the route with the driver's count and reports the differences
// against what the system expects.
func (s *TripService) Close(ctx context.Context, tripID int64, in CloseInput) (TripDetail, error) {
	var out TripDetail
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		trip, err := s.open(ctx, tripID)
		if err != nil {
			return err
		}
		if err := trip.Close(domain.Settlement{Date: s.clock.Today(), CashUSD: in.CashUSD, Returned: in.Returned, Note: in.Note}); err != nil {
			return err
		}
		if err := s.trips.Save(ctx, &trip); err != nil {
			return err
		}
		out, err = s.detail(ctx, trip)
		return err
	})
	return out, err
}

// ApproveSettlement is the office accepting the driver's count: what came back
// returns to the warehouse and the trip is settled.
func (s *TripService) ApproveSettlement(ctx context.Context, tripID int64) (domain.Trip, error) {
	var out domain.Trip
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		trip, err := s.trips.GetForUpdate(ctx, tripID)
		if err != nil {
			return err
		}
		if trip.Status != domain.TripToSettle || trip.Settlement == nil {
			return domain.Rule("Ese viaje no está esperando liquidación.")
		}
		credits, fresh := trip.PlanReturns(s.clock.Today())
		for _, c := range credits {
			if err := s.lots.AddQuantity(ctx, c.LotID, c.Quantity); err != nil {
				return err
			}
		}
		for i := range fresh {
			if err := s.lots.Create(ctx, &fresh[i]); err != nil {
				return err
			}
		}
		trip.Status = domain.TripSettled
		out = trip
		return s.trips.Save(ctx, &trip)
	})
	return out, err
}

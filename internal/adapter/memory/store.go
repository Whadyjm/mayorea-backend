// Package memory is an in-process implementation of the repository ports. It
// backs the use-case tests and lets the frontend run against the API without
// a database (STORAGE=memory). Data is lost on restart and transactions do
// not roll back, so use cases must validate before they write.
package memory

import (
	"context"
	"sync"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

type txKey struct{}

type Store struct {
	mu sync.Mutex

	products  map[int64]domain.Product // Physical is derived from lots
	lots      map[int64]domain.Lot
	customers map[int64]domain.Customer
	orders    map[int64]domain.Order
	invoices  map[int64]domain.Invoice
	trucks    map[int64]domain.Truck
	trips     map[int64]domain.Trip
	users     map[int64]domain.User
	sessions  map[string]domain.Session
	rate      *domain.ExchangeRate

	seq map[string]int64
}

func NewStore() *Store {
	return &Store{
		products: map[int64]domain.Product{}, lots: map[int64]domain.Lot{},
		customers: map[int64]domain.Customer{}, orders: map[int64]domain.Order{},
		invoices: map[int64]domain.Invoice{}, trucks: map[int64]domain.Truck{}, trips: map[int64]domain.Trip{},
		users: map[int64]domain.User{}, sessions: map[string]domain.Session{},
		seq: map[string]int64{},
	}
}

func (s *Store) next(name string) int64 {
	s.seq[name]++
	return s.seq[name]
}

// lock takes the store mutex unless ctx already runs inside Do.
func (s *Store) lock(ctx context.Context) func() {
	if ctx.Value(txKey{}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Do implements usecase.TxManager by serializing the work.
// Nested calls join the outer one, like the Postgres adapter.
func (s *Store) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txKey{}) != nil {
		return fn(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(context.WithValue(ctx, txKey{}, true))
}

func (s *Store) physical(productID int64) int {
	n := 0
	for _, l := range s.lots {
		if l.ProductID == productID {
			n += l.Quantity
		}
	}
	return n
}

func (s *Store) product(id int64) (domain.Product, bool) {
	p, ok := s.products[id]
	p.Physical = s.physical(id)
	return p, ok
}

func (s *Store) Products() usecase.ProductRepository   { return productRepo{s} }
func (s *Store) Lots() usecase.LotRepository           { return lotRepo{s} }
func (s *Store) Customers() usecase.CustomerRepository { return customerRepo{s} }
func (s *Store) Orders() usecase.OrderRepository       { return orderRepo{s} }
func (s *Store) Invoices() usecase.InvoiceRepository   { return invoiceRepo{s} }
func (s *Store) Trucks() usecase.TruckRepository       { return truckRepo{s} }
func (s *Store) Trips() usecase.TripRepository         { return tripRepo{s} }
func (s *Store) Rates()usecase.RateRepository         { return rateRepo{s} }

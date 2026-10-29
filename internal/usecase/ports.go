// Package usecase holds the application services. It depends only on the
// domain and on the ports declared here; adapters (Postgres, HTTP, memory)
// implement or consume them.
package usecase

import (
	"context"
	"time"

	"github.com/mayorea/backend/internal/domain"
)

type Clock interface {
	Today() time.Time // business calendar day, UTC midnight
	Now() time.Time   // the instant (sessions, expirations)
}

type UserRepository interface {
	// Create assigns ID; it fails with a Conflict error if the email is taken.
	Create(ctx context.Context, u *domain.User) error
	Get(ctx context.Context, id int64) (domain.User, error)
	FindByEmail(ctx context.Context, email string) (domain.User, bool, error)
	List(ctx context.Context) ([]domain.User, error)
	Count(ctx context.Context) (int, error)
	// Update writes name, role, active, truck and password hash.
	Update(ctx context.Context, u *domain.User) error
}

type SessionRepository interface {
	Create(ctx context.Context, s domain.Session) error
	Find(ctx context.Context, digest string) (domain.Session, bool, error)
	Delete(ctx context.Context, digest string) error
	DeleteByUser(ctx context.Context, userID int64) error
	DeleteExpired(ctx context.Context, now time.Time) error
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(hash, password string) bool
}

// Tokens makes opaque session tokens; only digests are persisted.
type Tokens interface {
	New() (token, digest string, err error)
	Digest(token string) string
}

// TxManager runs fn atomically. Repositories called with the ctx passed to fn
// join the same transaction.
type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type ProductRepository interface {
	Create(ctx context.Context, p *domain.Product) error
	List(ctx context.Context) ([]domain.Product, error)
	FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Product, error)
	// LockByIDs is FindByIDs with row locks held until the transaction ends.
	LockByIDs(ctx context.Context, ids []int64) (map[int64]domain.Product, error)
	AddCommitted(ctx context.Context, productID int64, delta int) error
}

type LotRepository interface {
	Create(ctx context.Context, l *domain.Lot) error
	ListByProduct(ctx context.Context, productID int64) ([]domain.Lot, error)
	// ListAvailable returns lots with stock for the given products.
	ListAvailable(ctx context.Context, productIDs []int64) ([]domain.Lot, error)
	LockAvailable(ctx context.Context, productIDs []int64) ([]domain.Lot, error)
	SetQuantity(ctx context.Context, lotID int64, qty int) error
	AddQuantity(ctx context.Context, lotID int64, delta int) error
}

type CustomerRepository interface {
	Create(ctx context.Context, c *domain.Customer) error
	Update(ctx context.Context, c *domain.Customer) error
	List(ctx context.Context) ([]domain.Customer, error)
	Get(ctx context.Context, id int64) (domain.Customer, error)
	FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Customer, error)
}

type OrderFilter struct {
	CustomerID int64 // 0 = any
	Statuses   []domain.OrderStatus
}

type OrderRepository interface {
	// Create assigns ID and Number.
	Create(ctx context.Context, o *domain.Order) error
	Get(ctx context.Context, id int64) (domain.Order, error)
	List(ctx context.Context, f OrderFilter) ([]domain.Order, error)
	FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Order, error)
	// UpdateStatus moves the order from -> to, or fails with a Conflict error
	// if the order is no longer in `from`.
	UpdateStatus(ctx context.Context, id int64, from, to domain.OrderStatus) error
}

type InvoiceFilter struct {
	CustomerID int64 // 0 = any
}

type InvoiceRepository interface {
	// Create assigns ID and the fiscal Number.
	Create(ctx context.Context, i *domain.Invoice) error
	Get(ctx context.Context, id int64) (domain.Invoice, error)
	// GetForUpdate is Get with a row lock held until the transaction ends.
	GetForUpdate(ctx context.Context, id int64) (domain.Invoice, error)
	List(ctx context.Context, f InvoiceFilter) ([]domain.Invoice, error)
	AddPayment(ctx context.Context, p *domain.Payment) error
	ListPaymentsByTrip(ctx context.Context, tripID int64) ([]domain.Payment, error)
}

type TripFilter struct {
	TruckID int64 // 0 = any
	Status  domain.TripStatus
}

type TripRepository interface {
	Create(ctx context.Context, t *domain.Trip) error
	Get(ctx context.Context, id int64) (domain.Trip, error)
	GetForUpdate(ctx context.Context, id int64) (domain.Trip, error)
	// FindOpen returns the truck's trip that is not settled yet, if any.
	FindOpen(ctx context.Context, truckID int64) (domain.Trip, bool, error)
	List(ctx context.Context, f TripFilter) ([]domain.Trip, error)
	// Save writes back the whole trip aggregate.
	Save(ctx context.Context, t *domain.Trip) error
}

type TruckRepository interface {
	Create(ctx context.Context, t *domain.Truck) error
	List(ctx context.Context) ([]domain.Truck, error)
	Get(ctx context.Context, id int64) (domain.Truck, error)
}

type RateRepository interface {
	// Current fails with a NotFound error when no rate was ever set.
	Current(ctx context.Context) (domain.ExchangeRate, error)
	Set(ctx context.Context, r domain.ExchangeRate) error
}

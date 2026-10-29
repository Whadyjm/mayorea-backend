// Package postgres implements the repository ports on PostgreSQL via pgx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

type txKey struct{}

// querier is what both *pgxpool.Pool and pgx.Tx offer.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type DB struct{ pool *pgxpool.Pool }

func Connect(ctx context.Context, url string) (*DB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (d *DB) Close() { d.pool.Close() }

func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// q returns the transaction carried by ctx, or the pool outside of one.
func (d *DB) q(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.pool
}

// Do implements usecase.TxManager. Nested calls join the outer transaction.
func (d *DB) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// Migrate applies the .sql files of fsys that were not applied yet, each in
// its own transaction, in filename order.
func (d *DB) Migrate(ctx context.Context, fsys fs.FS) error {
	if _, err := d.pool.Exec(ctx, `create table if not exists schema_migrations (name text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var done bool
		if err := d.pool.QueryRow(ctx, `select exists(select 1 from schema_migrations where name = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sqlText, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		err = d.Do(ctx, func(ctx context.Context) error {
			if _, err := d.q(ctx).Exec(ctx, string(sqlText)); err != nil {
				return err
			}
			_, err := d.q(ctx).Exec(ctx, `insert into schema_migrations (name) values ($1)`, name)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func (d *DB) Products() usecase.ProductRepository   { return productRepo{d} }
func (d *DB) Lots() usecase.LotRepository           { return lotRepo{d} }
func (d *DB) Customers() usecase.CustomerRepository { return customerRepo{d} }
func (d *DB) Orders() usecase.OrderRepository       { return orderRepo{d} }
func (d *DB) Invoices() usecase.InvoiceRepository   { return invoiceRepo{d} }
func (d *DB) Trucks() usecase.TruckRepository       { return truckRepo{d} }
func (d *DB) Trips() usecase.TripRepository         { return tripRepo{d} }
func (d *DB) Rates() usecase.RateRepository         { return rateRepo{d} }

// notFoundIf maps pgx.ErrNoRows to a domain NotFound error.
func notFoundIf(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound("%s no encontrado.", what)
	}
	return err
}

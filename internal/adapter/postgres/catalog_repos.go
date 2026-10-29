package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mayorea/backend/internal/domain"
)

// ---------- products ----------

type productRepo struct{ d *DB }

const productSelect = `
	select p.id, p.name, p.unit, p.price_usd, p.committed, p.min_stock,
	       coalesce((select sum(l.quantity) from lots l where l.product_id = p.id), 0)::int
	from products p`

func scanProduct(row pgx.Row) (domain.Product, error) {
	var p domain.Product
	err := row.Scan(&p.ID, &p.Name, &p.Unit, &p.PriceUSD, &p.Committed, &p.MinStock, &p.Physical)
	return p, err
}

func (r productRepo) Create(ctx context.Context, p *domain.Product) error {
	return r.d.q(ctx).QueryRow(ctx,
		`insert into products (name, unit, price_usd, min_stock) values ($1, $2, $3, $4) returning id`,
		p.Name, p.Unit, p.PriceUSD, p.MinStock).Scan(&p.ID)
}

func (r productRepo) List(ctx context.Context) ([]domain.Product, error) {
	rows, err := r.d.q(ctx).Query(ctx, productSelect+` order by p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r productRepo) find(ctx context.Context, ids []int64, suffix string) (map[int64]domain.Product, error) {
	rows, err := r.d.q(ctx).Query(ctx, productSelect+` where p.id = any($1) order by p.id`+suffix, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]domain.Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out[p.ID] = p
	}
	return out, rows.Err()
}

func (r productRepo) FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Product, error) {
	return r.find(ctx, ids, "")
}

// LockByIDs locks the product rows in id order (no deadlocks between
// concurrent orders) until the transaction ends.
func (r productRepo) LockByIDs(ctx context.Context, ids []int64) (map[int64]domain.Product, error) {
	return r.find(ctx, ids, ` for update of p`)
}

func (r productRepo) AddCommitted(ctx context.Context, id int64, delta int) error {
	tag, err := r.d.q(ctx).Exec(ctx, `update products set committed = committed + $2 where id = $1`, id, delta)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("Producto no encontrado.")
	}
	return nil
}

// ---------- lots ----------

type lotRepo struct{ d *DB }

const lotSelect = `select id, product_id, code, expires_on, quantity, location from lots`

func (r lotRepo) scan(ctx context.Context, sql string, args ...any) ([]domain.Lot, error) {
	rows, err := r.d.q(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Lot
	for rows.Next() {
		var l domain.Lot
		if err := rows.Scan(&l.ID, &l.ProductID, &l.Code, &l.ExpiresOn, &l.Quantity, &l.Location); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r lotRepo) Create(ctx context.Context, l *domain.Lot) error {
	return r.d.q(ctx).QueryRow(ctx,
		`insert into lots (product_id, code, expires_on, quantity, location) values ($1, $2, $3, $4, $5) returning id`,
		l.ProductID, l.Code, l.ExpiresOn, l.Quantity, l.Location).Scan(&l.ID)
}

func (r lotRepo) ListByProduct(ctx context.Context, productID int64) ([]domain.Lot, error) {
	return r.scan(ctx, lotSelect+` where product_id = $1 order by expires_on, id`, productID)
}

func (r lotRepo) ListAvailable(ctx context.Context, ids []int64) ([]domain.Lot, error) {
	return r.scan(ctx, lotSelect+` where product_id = any($1) and quantity > 0 order by product_id, expires_on, id`, ids)
}

func (r lotRepo) LockAvailable(ctx context.Context, ids []int64) ([]domain.Lot, error) {
	return r.scan(ctx, lotSelect+` where product_id = any($1) and quantity > 0 order by id for update`, ids)
}

func (r lotRepo) SetQuantity(ctx context.Context, id int64, qty int) error {
	tag, err := r.d.q(ctx).Exec(ctx, `update lots set quantity = $2 where id = $1`, id, qty)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("Lote no encontrado.")
	}
	return nil
}

func (r lotRepo) AddQuantity(ctx context.Context, id int64, delta int) error {
	tag, err := r.d.q(ctx).Exec(ctx, `update lots set quantity = quantity + $2 where id = $1`, id, delta)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("Lote no encontrado.")
	}
	return nil
}

// ---------- customers ----------

type customerRepo struct{ d *DB }

const customerSelect = `select id, name, rif, route, phone, credit_limit_usd, credit_days, address, lat, lng, coalesce(owner_id, 0) from customers`

func scanCustomer(row pgx.Row) (domain.Customer, error) {
	var c domain.Customer
	err := row.Scan(&c.ID, &c.Name, &c.RIF, &c.Route, &c.Phone, &c.CreditLimitUSD, &c.CreditDays, &c.Address, &c.Lat, &c.Lng, &c.OwnerID)
	return c, err
}

func (r customerRepo) Create(ctx context.Context, c *domain.Customer) error {
	err := r.d.q(ctx).QueryRow(ctx,
		`insert into customers (name, rif, route, phone, credit_limit_usd, credit_days, address, lat, lng, owner_id)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, 0)) returning id`,
		c.Name, c.RIF, c.Route, c.Phone, c.CreditLimitUSD, c.CreditDays, c.Address, c.Lat, c.Lng, c.OwnerID).Scan(&c.ID)
	if isUniqueViolation(err) {
		return domain.Conflict("Ya existe un cliente con el RIF %s.", c.RIF)
	}
	return err
}

func (r customerRepo) List(ctx context.Context) ([]domain.Customer, error) {
	rows, err := r.d.q(ctx).Query(ctx, customerSelect+` order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r customerRepo) Get(ctx context.Context, id int64) (domain.Customer, error) {
	c, err := scanCustomer(r.d.q(ctx).QueryRow(ctx, customerSelect+` where id = $1`, id))
	return c, notFoundIf(err, "Cliente")
}

func (r customerRepo) FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Customer, error) {
	rows, err := r.d.q(ctx).Query(ctx, customerSelect+` where id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]domain.Customer{}
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out[c.ID] = c
	}
	return out, rows.Err()
}

// ---------- trucks ----------

type truckRepo struct{ d *DB }

func (r truckRepo) Create(ctx context.Context, t *domain.Truck) error {
	routes := t.Routes
	if routes == nil {
		routes = []string{}
	}
	return r.d.q(ctx).QueryRow(ctx,
		`insert into trucks (name, driver, routes, customer_id) values ($1, $2, $3, nullif($4, 0)) returning id`,
		t.Name, t.Driver, routes, t.CustomerID).Scan(&t.ID)
}

func (r truckRepo) List(ctx context.Context) ([]domain.Truck, error) {
	rows, err := r.d.q(ctx).Query(ctx, `select id, name, driver, routes, coalesce(customer_id, 0) from trucks order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Truck
	for rows.Next() {
		var t domain.Truck
		if err := rows.Scan(&t.ID, &t.Name, &t.Driver, &t.Routes, &t.CustomerID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r truckRepo) Get(ctx context.Context, id int64) (domain.Truck, error) {
	var t domain.Truck
	err := r.d.q(ctx).QueryRow(ctx, `select id, name, driver, routes, coalesce(customer_id, 0) from trucks where id = $1`, id).
		Scan(&t.ID, &t.Name, &t.Driver, &t.Routes, &t.CustomerID)
	return t, notFoundIf(err, "Camión")
}

// ---------- exchange rate ----------

// The BCV rate of the day lives in the settings table (key bcv_rate); every client reads it from there
// through the API. Each change is also appended to exchange_rates as a history.
type rateRepo struct{ d *DB }

const bcvRateKey = "bcv_rate"

type rateValue struct {
	Rate        domain.Rate `json:"rate"`
	EffectiveOn string      `json:"effectiveOn"` // YYYY-MM-DD
	UpdatedAt   time.Time   `json:"updatedAt"`
	Source      string      `json:"source"`
}

func (r rateRepo) Current(ctx context.Context) (domain.ExchangeRate, error) {
	var raw []byte
	if err := r.d.q(ctx).QueryRow(ctx, `select value from settings where key = $1`, bcvRateKey).Scan(&raw); err != nil {
		return domain.ExchangeRate{}, notFoundIf(err, "Tasa BCV")
	}
	var v rateValue
	if err := json.Unmarshal(raw, &v); err != nil {
		return domain.ExchangeRate{}, fmt.Errorf("settings %s: %w", bcvRateKey, err)
	}
	on, err := time.Parse("2006-01-02", v.EffectiveOn)
	if err != nil {
		return domain.ExchangeRate{}, fmt.Errorf("settings %s: %w", bcvRateKey, err)
	}
	return domain.ExchangeRate{Rate: v.Rate, UpdatedOn: on, UpdatedAt: v.UpdatedAt, Source: v.Source}, nil
}

func (r rateRepo) Set(ctx context.Context, er domain.ExchangeRate) error {
	raw, err := json.Marshal(rateValue{Rate: er.Rate, EffectiveOn: er.UpdatedOn.Format("2006-01-02"), UpdatedAt: er.UpdatedAt.UTC(), Source: er.Source})
	if err != nil {
		return err
	}
	if _, err := r.d.q(ctx).Exec(ctx,
		`insert into settings (key, value) values ($1, $2)
		 on conflict (key) do update set value = $2, updated_at = now()`, bcvRateKey, raw); err != nil {
		return err
	}
	_, err = r.d.q(ctx).Exec(ctx, `insert into exchange_rates (rate, effective_on) values ($1, $2)`, er.Rate, er.UpdatedOn)
	return err
}

// ---------- company profiles ----------

// One row for the distributor (customer_id null) and one per commerce.
type companyRepo struct{ d *DB }

func (r companyRepo) Get(ctx context.Context, customerID int64) (domain.Company, bool, error) {
	var c domain.Company
	err := r.d.q(ctx).QueryRow(ctx,
		`select trade_name, legal_name, rif, phone, email, address, city, state, logo from company where customer_id is not distinct from $1::bigint`,
		truckArg(customerID)).
		Scan(&c.TradeName, &c.LegalName, &c.RIF, &c.Phone, &c.Email, &c.Address, &c.City, &c.State, &c.Logo)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Company{}, false, nil
	}
	return c, err == nil, err
}

func (r companyRepo) Save(ctx context.Context, customerID int64, c domain.Company) error {
	tag, err := r.d.q(ctx).Exec(ctx,
		`update company set trade_name = $1, legal_name = $2, rif = $3, phone = $4, email = $5,
		   address = $6, city = $7, state = $8, logo = $9, updated_at = now()
		 where customer_id is not distinct from $10::bigint`,
		c.TradeName, c.LegalName, c.RIF, c.Phone, c.Email, c.Address, c.City, c.State, c.Logo, truckArg(customerID))
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	_, err = r.d.q(ctx).Exec(ctx,
		`insert into company (trade_name, legal_name, rif, phone, email, address, city, state, logo, customer_id)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		c.TradeName, c.LegalName, c.RIF, c.Phone, c.Email, c.Address, c.City, c.State, c.Logo, truckArg(customerID))
	return err
}

func (r customerRepo) Update(ctx context.Context, c *domain.Customer) error {
	tag, err := r.d.q(ctx).Exec(ctx,
		`update customers set name = $2, rif = $3, route = $4, phone = $5, credit_limit_usd = $6, credit_days = $7,
		 address = $8, lat = $9, lng = $10 where id = $1`,
		c.ID, c.Name, c.RIF, c.Route, c.Phone, c.CreditLimitUSD, c.CreditDays, c.Address, c.Lat, c.Lng)
	if isUniqueViolation(err) {
		return domain.Conflict("Ya existe un cliente con el RIF %s.", c.RIF)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("Cliente no encontrado.")
	}
	return nil
}

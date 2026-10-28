package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

type tripRepo struct{ d *DB }

const tripSelect = `select id, truck_id, driver, trip_date, status, settled_on, coalesce(settlement_cash_usd, 0), settlement_note from trips`

// queryAll runs a query and maps every row, closing the rows before returning
// so the connection is free for the next statement of the same transaction.
func queryAll[T any](ctx context.Context, q querier, scan func(pgx.Rows) (T, error), sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r tripRepo) load(ctx context.Context, sql string, args ...any) ([]domain.Trip, error) {
	q := r.d.q(ctx)
	type head struct {
		trip      domain.Trip
		settledOn *time.Time
		cash      domain.Cents
		note      string
	}
	heads, err := queryAll(ctx, q, func(rows pgx.Rows) (head, error) {
		var h head
		err := rows.Scan(&h.trip.ID, &h.trip.TruckID, &h.trip.Driver, &h.trip.Date, &h.trip.Status, &h.settledOn, &h.cash, &h.note)
		return h, err
	}, sql, args...)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Trip, 0, len(heads))
	for _, h := range heads {
		t := h.trip
		if err := r.fillChildren(ctx, q, &t); err != nil {
			return nil, err
		}
		if h.settledOn != nil {
			t.Settlement = &domain.Settlement{Date: *h.settledOn, CashUSD: h.cash, Note: h.note}
			if t.Settlement.Returned, err = queryAll(ctx, q, func(rows pgx.Rows) (domain.Qty, error) {
				var v domain.Qty
				return v, rows.Scan(&v.ProductID, &v.Quantity)
			}, `select product_id, quantity from trip_returned where trip_id = $1 order by product_id`, t.ID); err != nil {
				return nil, err
			}
		}
		out = append(out, t)
	}
	return out, nil
}

func (r tripRepo) fillChildren(ctx context.Context, q querier, t *domain.Trip) error {
	var err error
	if t.Stops, err = queryAll(ctx, q, func(rows pgx.Rows) (int64, error) {
		var id int64
		return id, rows.Scan(&id)
	}, `select customer_id from trip_stops where trip_id = $1 order by position`, t.ID); err != nil {
		return err
	}
	type link struct {
		id        int64
		kind      string
		delivered bool
	}
	links, err := queryAll(ctx, q, func(rows pgx.Rows) (link, error) {
		var l link
		return l, rows.Scan(&l.id, &l.kind, &l.delivered)
	}, `select order_id, kind, delivered from trip_orders where trip_id = $1 order by position`, t.ID)
	if err != nil {
		return err
	}
	for _, l := range links {
		switch l.kind {
		case "dispatch":
			t.Orders = append(t.Orders, l.id)
			if l.delivered {
				t.Delivered = append(t.Delivered, l.id)
			}
		case "sale":
			t.Sales = append(t.Sales, l.id)
		case "presale":
			t.Presales = append(t.Presales, l.id)
		}
	}
	if t.Load, err = queryAll(ctx, q, func(rows pgx.Rows) (domain.Qty, error) {
		var v domain.Qty
		return v, rows.Scan(&v.ProductID, &v.Quantity)
	}, `select product_id, quantity from trip_load where trip_id = $1 order by position`, t.ID); err != nil {
		return err
	}
	t.Origin, err = queryAll(ctx, q, func(rows pgx.Rows) (domain.LotTake, error) {
		var v domain.LotTake
		return v, rows.Scan(&v.LotID, &v.ProductID, &v.Quantity, &v.ExpiresOn)
	}, `select o.lot_id, l.product_id, o.quantity, l.expires_on
		from trip_origin o join lots l on l.id = o.lot_id where o.trip_id = $1 order by o.lot_id`, t.ID)
	return err
}

func (r tripRepo) Create(ctx context.Context, t *domain.Trip) error {
	err := r.d.q(ctx).QueryRow(ctx,
		`insert into trips (truck_id, driver, trip_date, status) values ($1, $2, $3, $4) returning id`,
		t.TruckID, t.Driver, t.Date, t.Status).Scan(&t.ID)
	if isUniqueViolation(err) {
		return domain.Conflict("El camión ya tiene un viaje abierto.")
	}
	if err != nil {
		return err
	}
	return r.writeChildren(ctx, t)
}

// Save rewrites the aggregate: the header, and all child rows from scratch
// (a trip has tens of rows, so this stays simple and cheap).
func (r tripRepo) Save(ctx context.Context, t *domain.Trip) error {
	q := r.d.q(ctx)
	var settledOn *time.Time
	var cash *domain.Cents
	var note string
	if s := t.Settlement; s != nil {
		settledOn, cash, note = &s.Date, &s.CashUSD, s.Note
	}
	tag, err := q.Exec(ctx,
		`update trips set status = $2, settled_on = $3, settlement_cash_usd = $4, settlement_note = $5 where id = $1`,
		t.ID, t.Status, settledOn, cash, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("Viaje no encontrado.")
	}
	for _, table := range []string{"trip_stops", "trip_orders", "trip_load", "trip_origin", "trip_returned"} {
		if _, err := q.Exec(ctx, `delete from `+table+` where trip_id = $1`, t.ID); err != nil {
			return err
		}
	}
	return r.writeChildren(ctx, t)
}

func (r tripRepo) writeChildren(ctx context.Context, t *domain.Trip) error {
	q := r.d.q(ctx)
	exec := func(sql string, args ...any) error {
		_, err := q.Exec(ctx, sql, args...)
		return err
	}
	for i, c := range t.Stops {
		if err := exec(`insert into trip_stops (trip_id, position, customer_id) values ($1, $2, $3)`, t.ID, i, c); err != nil {
			return err
		}
	}
	pos := 0
	link := func(ids []int64, kind string, delivered func(int64) bool) error {
		for _, id := range ids {
			if err := exec(`insert into trip_orders (trip_id, order_id, kind, delivered, position) values ($1, $2, $3, $4, $5)`,
				t.ID, id, kind, delivered(id), pos); err != nil {
				return err
			}
			pos++
		}
		return nil
	}
	never := func(int64) bool { return false }
	if err := link(t.Orders, "dispatch", func(id int64) bool { return containsID(t.Delivered, id) }); err != nil {
		return err
	}
	if err := link(t.Sales, "sale", never); err != nil {
		return err
	}
	if err := link(t.Presales, "presale", never); err != nil {
		return err
	}
	for i, l := range t.Load {
		if err := exec(`insert into trip_load (trip_id, position, product_id, quantity) values ($1, $2, $3, $4)`, t.ID, i, l.ProductID, l.Quantity); err != nil {
			return err
		}
	}
	for _, o := range t.Origin {
		if err := exec(`insert into trip_origin (trip_id, lot_id, quantity) values ($1, $2, $3)`, t.ID, o.LotID, o.Quantity); err != nil {
			return err
		}
	}
	if t.Settlement != nil {
		for _, ret := range t.Settlement.Returned {
			if err := exec(`insert into trip_returned (trip_id, product_id, quantity) values ($1, $2, $3)`, t.ID, ret.ProductID, ret.Quantity); err != nil {
				return err
			}
		}
	}
	return nil
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func (r tripRepo) one(ctx context.Context, suffix string, id int64) (domain.Trip, error) {
	list, err := r.load(ctx, tripSelect+` where id = $1`+suffix, id)
	if err != nil {
		return domain.Trip{}, err
	}
	if len(list) == 0 {
		return domain.Trip{}, domain.NotFound("Viaje no encontrado.")
	}
	return list[0], nil
}

func (r tripRepo) Get(ctx context.Context, id int64) (domain.Trip, error) {
	return r.one(ctx, "", id)
}

func (r tripRepo) GetForUpdate(ctx context.Context, id int64) (domain.Trip, error) {
	return r.one(ctx, " for update", id)
}

func (r tripRepo) FindOpen(ctx context.Context, truckID int64) (domain.Trip, bool, error) {
	list, err := r.load(ctx, tripSelect+` where truck_id = $1 and status <> 'liquidado' limit 1 for update`, truckID)
	if err != nil {
		return domain.Trip{}, false, err
	}
	if len(list) == 0 {
		return domain.Trip{}, false, nil
	}
	return list[0], true, nil
}

func (r tripRepo) List(ctx context.Context, f usecase.TripFilter) ([]domain.Trip, error) {
	return r.load(ctx, tripSelect+`
		where ($1::bigint = 0 or truck_id = $1) and ($2::text = '' or status = $2)
		order by id desc`, f.TruckID, string(f.Status))
}

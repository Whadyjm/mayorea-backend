package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// ---------- orders ----------

type orderRepo struct{ d *DB }

const orderSelect = `select id, number, customer_id, order_date, salesperson, channel, terms, status, note from orders`

func (r orderRepo) Create(ctx context.Context, o *domain.Order) error {
	q := r.d.q(ctx)
	err := q.QueryRow(ctx,
		`insert into orders (customer_id, order_date, salesperson, channel, terms, status, note)
		 values ($1, $2, $3, $4, $5, $6, $7) returning id, number`,
		o.CustomerID, o.Date, o.Salesperson, o.Channel, o.Terms, o.Status, o.Note).Scan(&o.ID, &o.Number)
	if err != nil {
		return err
	}
	for _, l := range o.Lines {
		if _, err := q.Exec(ctx,
			`insert into order_lines (order_id, product_id, quantity, price_usd) values ($1, $2, $3, $4)`,
			o.ID, l.ProductID, l.Quantity, l.PriceUSD); err != nil {
			return err
		}
	}
	return nil
}

func (r orderRepo) load(ctx context.Context, sql string, args ...any) ([]domain.Order, error) {
	q := r.d.q(ctx)
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	var out []domain.Order
	idx := map[int64]int{}
	var ids []int64
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(&o.ID, &o.Number, &o.CustomerID, &o.Date, &o.Salesperson, &o.Channel, &o.Terms, &o.Status, &o.Note); err != nil {
			rows.Close()
			return nil, err
		}
		idx[o.ID] = len(out)
		ids = append(ids, o.ID)
		out = append(out, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	lines, err := q.Query(ctx, `select order_id, product_id, quantity, price_usd from order_lines where order_id = any($1) order by id`, ids)
	if err != nil {
		return nil, err
	}
	defer lines.Close()
	for lines.Next() {
		var oid int64
		var l domain.OrderLine
		if err := lines.Scan(&oid, &l.ProductID, &l.Quantity, &l.PriceUSD); err != nil {
			return nil, err
		}
		out[idx[oid]].Lines = append(out[idx[oid]].Lines, l)
	}
	return out, lines.Err()
}

func (r orderRepo) Get(ctx context.Context, id int64) (domain.Order, error) {
	orders, err := r.load(ctx, orderSelect+` where id = $1`, id)
	if err != nil {
		return domain.Order{}, err
	}
	if len(orders) == 0 {
		return domain.Order{}, domain.NotFound("Pedido no encontrado.")
	}
	return orders[0], nil
}

func (r orderRepo) List(ctx context.Context, f usecase.OrderFilter) ([]domain.Order, error) {
	statuses := make([]string, len(f.Statuses))
	for i, s := range f.Statuses {
		statuses[i] = string(s)
	}
	return r.load(ctx, orderSelect+`
		where ($1::bigint = 0 or customer_id = $1)
		  and (cardinality($2::text[]) = 0 or status = any($2))
		order by id`, f.CustomerID, statuses)
}

func (r orderRepo) FindByIDs(ctx context.Context, ids []int64) (map[int64]domain.Order, error) {
	orders, err := r.load(ctx, orderSelect+` where id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]domain.Order, len(orders))
	for _, o := range orders {
		out[o.ID] = o
	}
	return out, nil
}

func (r orderRepo) UpdateStatus(ctx context.Context, id int64, from, to domain.OrderStatus) error {
	q := r.d.q(ctx)
	tag, err := q.Exec(ctx, `update orders set status = $3 where id = $1 and status = $2`, id, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var exists bool
	if err := q.QueryRow(ctx, `select exists(select 1 from orders where id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.NotFound("Pedido no encontrado.")
	}
	return domain.Conflict("El pedido ya no está en estado %q.", from)
}

// ---------- invoices ----------

type invoiceRepo struct{ d *DB }

const invoiceSelect = `select id, number, order_id, customer_id, issued_on, due_on, total_usd, issue_rate from invoices`

func (r invoiceRepo) Create(ctx context.Context, i *domain.Invoice) error {
	return r.d.q(ctx).QueryRow(ctx,
		`insert into invoices (order_id, customer_id, issued_on, due_on, total_usd, issue_rate)
		 values ($1, $2, $3, $4, $5, $6) returning id, number`,
		i.OrderID, i.CustomerID, i.IssuedOn, i.DueOn, i.TotalUSD, i.IssueRate).Scan(&i.ID, &i.Number)
}

func (r invoiceRepo) load(ctx context.Context, sql string, args ...any) ([]domain.Invoice, error) {
	q := r.d.q(ctx)
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	var out []domain.Invoice
	idx := map[int64]int{}
	var ids []int64
	for rows.Next() {
		var i domain.Invoice
		if err := rows.Scan(&i.ID, &i.Number, &i.OrderID, &i.CustomerID, &i.IssuedOn, &i.DueOn, &i.TotalUSD, &i.IssueRate); err != nil {
			rows.Close()
			return nil, err
		}
		idx[i.ID] = len(out)
		ids = append(ids, i.ID)
		out = append(out, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	pays, err := q.Query(ctx, `select id, invoice_id, paid_on, cash_usd, bs, rate, reference, applied_usd, exchange_diff_bs, coalesce(trip_id, 0)
		from payments where invoice_id = any($1) order by id`, ids)
	if err != nil {
		return nil, err
	}
	defer pays.Close()
	for pays.Next() {
		var p domain.Payment
		if err := pays.Scan(&p.ID, &p.InvoiceID, &p.Date, &p.CashUSD, &p.Bs, &p.Rate, &p.Reference, &p.AppliedUSD, &p.ExchangeDiffBs, &p.TripID); err != nil {
			return nil, err
		}
		out[idx[p.InvoiceID]].Payments = append(out[idx[p.InvoiceID]].Payments, p)
	}
	return out, pays.Err()
}

func (r invoiceRepo) one(ctx context.Context, suffix string, id int64) (domain.Invoice, error) {
	list, err := r.load(ctx, invoiceSelect+` where id = $1`+suffix, id)
	if err != nil {
		return domain.Invoice{}, err
	}
	if len(list) == 0 {
		return domain.Invoice{}, domain.NotFound("Factura no encontrada.")
	}
	return list[0], nil
}

func (r invoiceRepo) Get(ctx context.Context, id int64) (domain.Invoice, error) {
	return r.one(ctx, "", id)
}

func (r invoiceRepo) GetForUpdate(ctx context.Context, id int64) (domain.Invoice, error) {
	return r.one(ctx, " for update", id)
}

func (r invoiceRepo) List(ctx context.Context, f usecase.InvoiceFilter) ([]domain.Invoice, error) {
	return r.load(ctx, invoiceSelect+` where ($1::bigint = 0 or customer_id = $1) order by id desc`, f.CustomerID)
}

func (r invoiceRepo) AddPayment(ctx context.Context, p *domain.Payment) error {
	var trip *int64
	if p.TripID != 0 {
		trip = &p.TripID
	}
	return r.d.q(ctx).QueryRow(ctx,
		`insert into payments (invoice_id, paid_on, cash_usd, bs, rate, reference, applied_usd, exchange_diff_bs, trip_id)
		 values ($1, $2, $3, $4, $5, $6, $7, $8, $9) returning id`,
		p.InvoiceID, p.Date, p.CashUSD, p.Bs, p.Rate, p.Reference, p.AppliedUSD, p.ExchangeDiffBs, trip).Scan(&p.ID)
}

func (r invoiceRepo) ListPaymentsByTrip(ctx context.Context, tripID int64) ([]domain.Payment, error) {
	return queryAll(ctx, r.d.q(ctx), func(rows pgx.Rows) (domain.Payment, error) {
		var p domain.Payment
		err := rows.Scan(&p.ID, &p.InvoiceID, &p.Date, &p.CashUSD, &p.Bs, &p.Rate, &p.Reference, &p.AppliedUSD, &p.ExchangeDiffBs)
		p.TripID = tripID
		return p, err
	}, `select id, invoice_id, paid_on, cash_usd, bs, rate, reference, applied_usd, exchange_diff_bs
		from payments where trip_id = $1 order by id`, tripID)
}

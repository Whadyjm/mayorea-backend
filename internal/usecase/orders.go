package usecase

import (
	"context"
	"sort"

	"github.com/mayorea/backend/internal/domain"
)

// OrderService takes pedidos, reserves stock and gates them by credit.
type OrderService struct {
	tx        TxManager
	orders    OrderRepository
	customers CustomerRepository
	products  ProductRepository
	invoices  InvoiceRepository
	clock     Clock
}

func NewOrderService(tx TxManager, o OrderRepository, c CustomerRepository, p ProductRepository, i InvoiceRepository, clock Clock) *OrderService {
	return &OrderService{tx: tx, orders: o, customers: c, products: p, invoices: i, clock: clock}
}

var openStatuses = []domain.OrderStatus{domain.StatusPending, domain.StatusApproved}

func (s *OrderService) credit(ctx context.Context, c domain.Customer) (domain.CreditStatus, error) {
	invoices, err := s.invoices.List(ctx, InvoiceFilter{CustomerID: c.ID})
	if err != nil {
		return domain.CreditStatus{}, err
	}
	open, err := s.orders.List(ctx, OrderFilter{CustomerID: c.ID, Statuses: openStatuses})
	if err != nil {
		return domain.CreditStatus{}, err
	}
	return domain.AssessCredit(c, invoices, open, s.clock.Today()), nil
}

func (s *OrderService) CustomerCredit(ctx context.Context, customerID int64) (domain.CreditStatus, error) {
	c, err := s.customers.Get(ctx, customerID)
	if err != nil {
		return domain.CreditStatus{}, err
	}
	return s.credit(ctx, c)
}

// Create validates the draft, reserves the stock and stores the order pending
// approval. Reservation and order are written atomically with the product rows
// locked, so concurrent orders cannot oversell.
func (s *OrderService) Create(ctx context.Context, draft domain.OrderDraft) (domain.Order, error) {
	var out domain.Order
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		c, err := s.customers.Get(ctx, draft.CustomerID)
		if err != nil {
			return err
		}
		st, err := s.credit(ctx, c)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(draft.Lines))
		for _, l := range draft.Lines {
			ids = append(ids, l.ProductID)
		}
		products, err := s.products.LockByIDs(ctx, ids)
		if err != nil {
			return err
		}
		order, err := domain.NewOrder(draft, c, st, products, s.clock.Today())
		if err != nil {
			return err
		}
		if err := s.orders.Create(ctx, &order); err != nil {
			return err
		}
		for pid, qty := range order.QuantityByProduct() {
			if err := s.products.AddCommitted(ctx, pid, qty); err != nil {
				return err
			}
		}
		out = order
		return nil
	})
	return out, err
}

func (s *OrderService) Get(ctx context.Context, id int64) (domain.Order, error) {
	return s.orders.Get(ctx, id)
}

func (s *OrderService) List(ctx context.Context, f OrderFilter) ([]domain.Order, error) {
	orders, err := s.orders.List(ctx, f)
	if err != nil {
		return nil, err
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].ID > orders[j].ID })
	return orders, nil
}

type OrderReview struct {
	Alert  domain.CreditAlert
	Credit domain.CreditStatus
}

// Review gives the credit officer what they need before approving.
func (s *OrderService) Review(ctx context.Context, id int64) (OrderReview, error) {
	o, err := s.orders.Get(ctx, id)
	if err != nil {
		return OrderReview{}, err
	}
	c, err := s.customers.Get(ctx, o.CustomerID)
	if err != nil {
		return OrderReview{}, err
	}
	st, err := s.credit(ctx, c)
	if err != nil {
		return OrderReview{}, err
	}
	return OrderReview{Alert: domain.ReviewOrder(o, c, st), Credit: st}, nil
}

func (s *OrderService) Approve(ctx context.Context, id int64) (domain.Order, error) {
	if err := s.orders.UpdateStatus(ctx, id, domain.StatusPending, domain.StatusApproved); err != nil {
		return domain.Order{}, err
	}
	return s.orders.Get(ctx, id)
}

// Reject cancels a pending order and releases its reserved stock.
func (s *OrderService) Reject(ctx context.Context, id int64) (domain.Order, error) {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		o, err := s.orders.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := s.orders.UpdateStatus(ctx, id, domain.StatusPending, domain.StatusRejected); err != nil {
			return err
		}
		for pid, qty := range o.QuantityByProduct() {
			if err := s.products.AddCommitted(ctx, pid, -qty); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.Order{}, err
	}
	return s.orders.Get(ctx, id)
}

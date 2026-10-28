package usecase

import (
	"context"
	"strings"
	"time"

	"github.com/mayorea/backend/internal/domain"
)

// CatalogService manages master data: products, stock lots, customers,
// trucks and the BCV exchange rate.
type CatalogService struct {
	products  ProductRepository
	lots      LotRepository
	customers CustomerRepository
	trucks    TruckRepository
	rates     RateRepository
	clock     Clock
}

func NewCatalogService(p ProductRepository, l LotRepository, c CustomerRepository, t TruckRepository, r RateRepository, clock Clock) *CatalogService {
	return &CatalogService{products: p, lots: l, customers: c, trucks: t, rates: r, clock: clock}
}

type NewProduct struct {
	Name     string
	Unit     domain.Unit
	PriceUSD domain.Cents
	MinStock int
}

func (s *CatalogService) CreateProduct(ctx context.Context, in NewProduct) (domain.Product, error) {
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return domain.Product{}, domain.Invalid("Escribe el nombre del producto.")
	case !in.Unit.Valid():
		return domain.Product{}, domain.Invalid("Unidad inválida: usa bulto, caja o unidad.")
	case in.PriceUSD < 0 || in.MinStock < 0:
		return domain.Product{}, domain.Invalid("El precio y el mínimo no pueden ser negativos.")
	}
	p := domain.Product{Name: name, Unit: in.Unit, PriceUSD: in.PriceUSD, MinStock: in.MinStock}
	if err := s.products.Create(ctx, &p); err != nil {
		return domain.Product{}, err
	}
	return p, nil
}

func (s *CatalogService) ListProducts(ctx context.Context) ([]domain.Product, error) {
	return s.products.List(ctx)
}

type NewLot struct {
	ProductID int64
	Code      string
	ExpiresOn time.Time
	Quantity  int
	Location  string
}

// ReceiveLot registers incoming stock for a product.
func (s *CatalogService) ReceiveLot(ctx context.Context, in NewLot) (domain.Lot, error) {
	switch {
	case strings.TrimSpace(in.Code) == "":
		return domain.Lot{}, domain.Invalid("Escribe el código del lote.")
	case in.Quantity <= 0:
		return domain.Lot{}, domain.Invalid("La cantidad debe ser mayor a cero.")
	case in.ExpiresOn.IsZero():
		return domain.Lot{}, domain.Invalid("Indica la fecha de vencimiento.")
	}
	found, err := s.products.FindByIDs(ctx, []int64{in.ProductID})
	if err != nil {
		return domain.Lot{}, err
	}
	if _, ok := found[in.ProductID]; !ok {
		return domain.Lot{}, domain.NotFound("Producto no encontrado.")
	}
	l := domain.Lot{
		ProductID: in.ProductID, Code: strings.TrimSpace(in.Code), ExpiresOn: domain.DateOf(in.ExpiresOn),
		Quantity: in.Quantity, Location: strings.TrimSpace(in.Location),
	}
	if err := s.lots.Create(ctx, &l); err != nil {
		return domain.Lot{}, err
	}
	return l, nil
}

func (s *CatalogService) ListLots(ctx context.Context, productID int64) ([]domain.Lot, error) {
	return s.lots.ListByProduct(ctx, productID)
}

type NewCustomer struct {
	Name           string
	RIF            string
	Route          string
	Phone          string
	CreditLimitUSD domain.Cents
	CreditDays     int
}

func (s *CatalogService) CreateCustomer(ctx context.Context, in NewCustomer) (domain.Customer, error) {
	switch {
	case strings.TrimSpace(in.Name) == "":
		return domain.Customer{}, domain.Invalid("Escribe el nombre del cliente.")
	case !domain.ValidRIF(in.RIF):
		return domain.Customer{}, domain.Invalid("El RIF debe verse así: J-12345678-9.")
	case in.CreditLimitUSD < 0 || in.CreditDays < 0:
		return domain.Customer{}, domain.Invalid("El límite y los días de crédito no pueden ser negativos.")
	}
	c := domain.Customer{
		Name: strings.TrimSpace(in.Name), RIF: domain.NormalizeRIF(in.RIF), Route: strings.TrimSpace(in.Route),
		Phone: strings.TrimSpace(in.Phone), CreditLimitUSD: in.CreditLimitUSD, CreditDays: in.CreditDays,
	}
	if err := s.customers.Create(ctx, &c); err != nil {
		return domain.Customer{}, err
	}
	return c, nil
}

func (s *CatalogService) ListCustomers(ctx context.Context) ([]domain.Customer, error) {
	return s.customers.List(ctx)
}

type NewTruck struct {
	Name   string
	Driver string
	Routes []string
}

func (s *CatalogService) CreateTruck(ctx context.Context, in NewTruck) (domain.Truck, error) {
	if strings.TrimSpace(in.Name) == "" {
		return domain.Truck{}, domain.Invalid("Escribe el nombre del camión.")
	}
	t := domain.Truck{Name: strings.TrimSpace(in.Name), Driver: strings.TrimSpace(in.Driver), Routes: in.Routes}
	if err := s.trucks.Create(ctx, &t); err != nil {
		return domain.Truck{}, err
	}
	return t, nil
}

func (s *CatalogService) ListTrucks(ctx context.Context) ([]domain.Truck, error) {
	return s.trucks.List(ctx)
}

func (s *CatalogService) CurrentRate(ctx context.Context) (domain.ExchangeRate, error) {
	return s.rates.Current(ctx)
}

// SetRate records the BCV rate of the day (ten-thousandths of Bs per USD).
func (s *CatalogService) SetRate(ctx context.Context, rate domain.Rate) (domain.ExchangeRate, error) {
	if rate <= 0 {
		return domain.ExchangeRate{}, domain.Invalid("La tasa debe ser mayor a cero.")
	}
	r := domain.ExchangeRate{Rate: rate, UpdatedOn: s.clock.Today()}
	if err := s.rates.Set(ctx, r); err != nil {
		return domain.ExchangeRate{}, err
	}
	return r, nil
}

package usecase

import (
	"context"

	"github.com/mayorea/backend/internal/domain"
)

// CompanyRepository keeps company profiles: the distributor's (customerID 0)
// and one per commerce (customerID of the customer it belongs to).
type CompanyRepository interface {
	// Get returns the profile, or ok=false when it was never filled in.
	Get(ctx context.Context, customerID int64) (c domain.Company, ok bool, err error)
	Save(ctx context.Context, customerID int64, c domain.Company) error
}

type CompanyService struct {
	repo      CompanyRepository
	customers CustomerRepository
}

func NewCompanyService(r CompanyRepository, c CustomerRepository) *CompanyService {
	return &CompanyService{repo: r, customers: c}
}

// Get returns a profile. A distributor that has not filled it in yet gets a blank
// one; a commerce that has not gets what the office registered for it.
func (s *CompanyService) Get(ctx context.Context, customerID int64) (domain.Company, error) {
	c, ok, err := s.repo.Get(ctx, customerID)
	if err != nil || ok || customerID == 0 {
		return c, err
	}
	cust, err := s.customers.Get(ctx, customerID)
	if err != nil {
		return domain.Company{}, err
	}
	return domain.Company{TradeName: cust.Name, LegalName: cust.Name, RIF: cust.RIF, Phone: cust.Phone}, nil
}

// Save validates and replaces a profile. For a commerce, the customer record the
// office and the orders use is kept in step (name, RIF, phone).
func (s *CompanyService) Save(ctx context.Context, customerID int64, in domain.Company) (domain.Company, error) {
	c := in.Clean()
	if err := c.Validate(); err != nil {
		return domain.Company{}, err
	}
	if customerID != 0 {
		cust, err := s.customers.Get(ctx, customerID)
		if err != nil {
			return domain.Company{}, err
		}
		cust.Name, cust.RIF, cust.Phone = c.TradeName, c.RIF, c.Phone
		if err := s.customers.Update(ctx, &cust); err != nil {
			return domain.Company{}, err
		}
	}
	if err := s.repo.Save(ctx, customerID, c); err != nil {
		return domain.Company{}, err
	}
	return c, nil
}

package domain

import "time"

type Unit string

const (
	UnitBulk Unit = "bulto"
	UnitBox  Unit = "caja"
	UnitEach Unit = "unidad"
)

func (u Unit) Valid() bool { return u == UnitBulk || u == UnitBox || u == UnitEach }

type Product struct {
	ID        int64
	Name      string
	Unit      Unit
	PriceUSD  Cents
	Committed int // reserved by orders not yet dispatched
	MinStock  int
	Physical  int // sum of lot quantities; filled by repositories (read model)
}

// Available is the stock that can still be promised to new orders.
func (p Product) Available() int { return p.Physical - p.Committed }

type Lot struct {
	ID        int64
	ProductID int64
	Code      string
	ExpiresOn time.Time
	Quantity  int
	Location  string // aisle-rack
}

type Customer struct {
	ID             int64
	Name           string
	RIF            string
	Route          string
	Phone          string
	CreditLimitUSD Cents
	CreditDays     int
	Address        string
	Lat, Lng       *float64 // where it is; both set or both nil
	OwnerID        int64    // the commerce this client belongs to; 0 for the distributor's customers
}

type Truck struct {
	ID         int64
	Name       string
	Driver     string
	Routes     []string
	CustomerID int64 // the commerce that owns it; 0 for the distributor's own fleet
}

func (t Truck) Serves(route string) bool {
	for _, r := range t.Routes {
		if r == route {
			return true
		}
	}
	return false
}

type ExchangeRate struct {
	Rate      Rate
	UpdatedOn time.Time // business day it applies to
	UpdatedAt time.Time // exact instant it was set
	Source    string    // where it came from: RateSourceManual or RateSourceFeed
}

// Where an exchange rate came from.
const (
	RateSourceManual = "manual"   // typed in by someone with permission
	RateSourceFeed   = "dolarapi" // fetched from the public BCV feed
)

package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type Role string

const (
	RoleOwner       Role = "admin"       // sees and does everything
	RoleCredit      Role = "credito"     // credit approval and collections
	RoleWarehouse   Role = "galpon"      // stock, picking and dispatch
	RoleSalesperson Role = "preventista" // takes orders
	RoleDriver      Role = "chofer"      // runs a truck on the street
	RoleShop        Role = "comercio"    // a customer business signing in to its own account
)

type Permission string

const (
	PermCatalogBrowse    Permission = "catalog.browse" // products, prices and the BCV rate; also held by commerce accounts
	PermCatalogRead      Permission = "catalog.read"
	PermCatalogWrite     Permission = "catalog.write" // products, lots, trucks
	PermCustomersWrite   Permission = "customers.write"
	PermCustomersEdit    Permission = "customers.edit" // change a customer's data; a commerce only its own contact data
	PermRateWrite        Permission = "rate.write"
	PermOrdersRead       Permission = "orders.read"
	PermOrdersCreate     Permission = "orders.create"
	PermOrdersApprove    Permission = "orders.approve"
	PermDispatchManage   Permission = "dispatch.manage" // load plan, dispatch, depart
	PermCollectionsRead  Permission = "collections.read"
	PermCollectionsWrite Permission = "collections.register"
	PermTripsDepart      Permission = "trips.depart" // send a truck out; drivers only their own
	PermTripsRead        Permission = "trips.read"
	PermTripsOperate     Permission = "trips.operate" // the driver's actions on a trip
	PermTripsSettle      Permission = "trips.settle"  // office approves the driver's count
	PermUsersManage      Permission = "users.manage"
	PermCompanyWrite     Permission = "company.write" // edit the own profile: the distributor's (admin) or a commerce's
	PermFleetManage      Permission = "fleet.manage"  // create and list trucks and their drivers; a commerce only its own
)

var allPermissions = []Permission{
	PermCatalogBrowse, PermCatalogRead, PermCatalogWrite, PermCustomersWrite, PermCustomersEdit, PermRateWrite,
	PermOrdersRead, PermOrdersCreate, PermOrdersApprove, PermDispatchManage,
	PermCollectionsRead, PermCollectionsWrite, PermTripsDepart, PermTripsRead, PermTripsOperate,
	PermTripsSettle, PermUsersManage, PermCompanyWrite, PermFleetManage,
}

var rolePermissions = map[Role][]Permission{
	RoleOwner: allPermissions,
	RoleCredit: {
		PermCatalogBrowse, PermCatalogRead, PermCustomersWrite, PermCustomersEdit, PermRateWrite, PermOrdersRead, PermOrdersApprove,
		PermCollectionsRead, PermCollectionsWrite, PermTripsRead, PermTripsSettle,
	},
	RoleWarehouse: {
		PermCatalogBrowse, PermCatalogRead, PermCatalogWrite, PermOrdersRead, PermDispatchManage, PermTripsDepart, PermTripsRead,
	},
	RoleSalesperson: {
		PermCatalogBrowse, PermCatalogRead, PermOrdersRead, PermOrdersCreate,
	},
	RoleDriver: {
		PermCatalogBrowse, PermCatalogRead, PermCollectionsRead, PermTripsDepart, PermTripsRead, PermTripsOperate,
	},
	// A commerce works only on its own account: the handlers scope orders, invoices
	// and the customer list to Principal.CustomerID.
	RoleShop: {
		PermCatalogBrowse, PermCustomersEdit, PermOrdersRead, PermOrdersCreate, PermCollectionsRead, PermCompanyWrite, PermFleetManage,
	},
}

func (r Role) Valid() bool { _, ok := rolePermissions[r]; return ok }

func (r Role) Can(p Permission) bool {
	for _, x := range rolePermissions[r] {
		if x == p {
			return true
		}
	}
	return false
}

func (r Role) Permissions() []Permission { return append([]Permission(nil), rolePermissions[r]...) }

type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Role         Role
	Active       bool
	TruckID      int64 // drivers only: the truck they run; 0 otherwise
	CustomerID   int64 // commerces only: the customer they sign in for; 0 otherwise
}

// Principal is the authenticated caller, as seen by authorization checks.
type Principal struct {
	UserID     int64
	Name       string
	Role       Role
	TruckID    int64
	CustomerID int64
}

func (p Principal) Can(perm Permission) bool { return p.Role.Can(perm) }

func (u User) Principal() Principal {
	return Principal{UserID: u.ID, Name: u.Name, Role: u.Role, TruckID: u.TruckID, CustomerID: u.CustomerID}
}

// Session is a login. Only the digest of the token is stored.
type Session struct {
	Digest    string
	UserID    int64
	ExpiresAt time.Time
}

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// ValidatePassword enforces a minimum length, and bcrypt's 72-byte ceiling.
func ValidatePassword(p string) error {
	if utf8.RuneCountInString(p) < 8 {
		return Invalid("La contraseña debe tener al menos 8 caracteres.")
	}
	if len(p) > 72 {
		return Invalid("La contraseña es demasiado larga (máximo 72 bytes).")
	}
	return nil
}

// ValidateUserFields checks the data shared by creation and edition.
func ValidateUserFields(name string, role Role, truckID, customerID int64) error {
	switch {
	case strings.TrimSpace(name) == "":
		return Invalid("Escribe el nombre de la persona.")
	case !role.Valid():
		return Invalid("Rol inválido.")
	case role == RoleDriver && truckID <= 0:
		return Invalid("Un chofer necesita un camión asignado.")
	case role != RoleDriver && truckID != 0:
		return Invalid("Solo los choferes tienen camión asignado.")
	case role == RoleShop && customerID <= 0:
		return Invalid("Una cuenta de comercio necesita el comercio al que pertenece.")
	case role != RoleShop && customerID != 0:
		return Invalid("Solo las cuentas de comercio se vinculan a un comercio.")
	}
	return nil
}

func ValidEmail(e string) bool { return emailRe.MatchString(NormalizeEmail(e)) }

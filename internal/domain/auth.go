package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type Role string

const (
	RoleOwner       Role = "dueno"       // sees and does everything
	RoleCredit      Role = "credito"     // credit approval and collections
	RoleWarehouse   Role = "galpon"      // stock, picking and dispatch
	RoleSalesperson Role = "preventista" // takes orders
	RoleDriver      Role = "chofer"      // runs a truck on the street
)

type Permission string

const (
	PermCatalogRead      Permission = "catalog.read"
	PermCatalogWrite     Permission = "catalog.write" // products, lots, trucks
	PermCustomersWrite   Permission = "customers.write"
	PermRateWrite        Permission = "rate.write"
	PermOrdersRead       Permission = "orders.read"
	PermOrdersCreate     Permission = "orders.create"
	PermOrdersApprove    Permission = "orders.approve"
	PermDispatchManage   Permission = "dispatch.manage" // load plan, dispatch, depart
	PermCollectionsRead  Permission = "collections.read"
	PermCollectionsWrite Permission = "collections.register"
	PermTripsRead        Permission = "trips.read"
	PermTripsOperate     Permission = "trips.operate" // the driver's actions on a trip
	PermTripsSettle      Permission = "trips.settle"  // office approves the driver's count
	PermUsersManage      Permission = "users.manage"
)

var allPermissions = []Permission{
	PermCatalogRead, PermCatalogWrite, PermCustomersWrite, PermRateWrite,
	PermOrdersRead, PermOrdersCreate, PermOrdersApprove, PermDispatchManage,
	PermCollectionsRead, PermCollectionsWrite, PermTripsRead, PermTripsOperate,
	PermTripsSettle, PermUsersManage,
}

var rolePermissions = map[Role][]Permission{
	RoleOwner: allPermissions,
	RoleCredit: {
		PermCatalogRead, PermCustomersWrite, PermRateWrite, PermOrdersRead, PermOrdersApprove,
		PermCollectionsRead, PermCollectionsWrite, PermTripsRead, PermTripsSettle,
	},
	RoleWarehouse: {
		PermCatalogRead, PermCatalogWrite, PermOrdersRead, PermDispatchManage, PermTripsRead,
	},
	RoleSalesperson: {
		PermCatalogRead, PermOrdersRead, PermOrdersCreate,
	},
	RoleDriver: {
		PermCatalogRead, PermCollectionsRead, PermTripsRead, PermTripsOperate,
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
}

// Principal is the authenticated caller, as seen by authorization checks.
type Principal struct {
	UserID  int64
	Name    string
	Role    Role
	TruckID int64
}

func (p Principal) Can(perm Permission) bool { return p.Role.Can(perm) }

func (u User) Principal() Principal {
	return Principal{UserID: u.ID, Name: u.Name, Role: u.Role, TruckID: u.TruckID}
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
func ValidateUserFields(name string, role Role, truckID int64) error {
	switch {
	case strings.TrimSpace(name) == "":
		return Invalid("Escribe el nombre de la persona.")
	case !role.Valid():
		return Invalid("Rol inválido.")
	case role == RoleDriver && truckID <= 0:
		return Invalid("Un chofer necesita un camión asignado.")
	case role != RoleDriver && truckID != 0:
		return Invalid("Solo los choferes tienen camión asignado.")
	}
	return nil
}

func ValidEmail(e string) bool { return emailRe.MatchString(NormalizeEmail(e)) }

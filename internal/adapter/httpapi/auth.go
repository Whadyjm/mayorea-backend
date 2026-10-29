package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

type principalKey struct{}

func principalFrom(ctx context.Context) (domain.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(domain.Principal)
	return p, ok
}

// shopScope reports the customer a commerce account is limited to. Handlers use
// it to narrow lists and to hide records of other customers (as "not found").
func shopScope(r *http.Request) (customerID int64, scoped bool) {
	p, _ := principalFrom(r.Context())
	return p.CustomerID, p.Role == domain.RoleShop
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// require authenticates the caller and checks the permission before running
// next. An empty permission only demands a valid session.
func (h handlers) require(perm domain.Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.Auth.Authenticate(r.Context(), bearerToken(r))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mayorea"`)
			writeError(w, r, err)
			return
		}
		if perm != "" && !p.Can(perm) {
			writeError(w, r, domain.Forbidden("No tienes permiso para hacer esto."))
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	}
}

// ownTruck restricts drivers to the truck assigned to them (the {id} in the path).
func (h handlers) ownTruck(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := principalFrom(r.Context())
		if p.Role == domain.RoleDriver {
			id, err := pathID(r, "id")
			if err != nil {
				writeError(w, r, err)
				return
			}
			if p.TruckID == 0 || id != p.TruckID {
				writeError(w, r, domain.Forbidden("Ese no es tu camión."))
				return
			}
		}
		next(w, r)
	}
}

// ownTrip restricts drivers to the trip of their own truck; everyone else
// who got past require() (owner, credit...) may act on any trip.
func (h handlers) ownTrip(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := principalFrom(r.Context())
		if p.Role == domain.RoleDriver {
			id, err := pathID(r, "id")
			if err != nil {
				writeError(w, r, err)
				return
			}
			trip, err := h.Trips.Get(r.Context(), id)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if p.TruckID == 0 || trip.TruckID != p.TruckID {
				writeError(w, r, domain.Forbidden("Ese viaje no es de tu camión."))
				return
			}
		}
		next(w, r)
	}
}

// ---------- DTOs ----------

type userDTO struct {
	ID         int64               `json:"id"`
	Name       string              `json:"name"`
	Email      string              `json:"email"`
	Role       domain.Role         `json:"role"`
	Active     bool                `json:"active"`
	TruckID    *int64              `json:"truckId"`
	CustomerID *int64              `json:"customerId"`
	Perms      []domain.Permission `json:"permissions,omitempty"`
}

func toUser(u domain.User) userDTO {
	d := userDTO{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, Active: u.Active}
	if u.TruckID != 0 {
		d.TruckID = &u.TruckID
	}
	if u.CustomerID != 0 {
		d.CustomerID = &u.CustomerID
	}
	return d
}

func toUserWithPerms(u domain.User) userDTO {
	d := toUser(u)
	d.Perms = u.Role.Permissions()
	return d
}

// ---------- handlers ----------

func (h handlers) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := h.Auth.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": res.Token, "expiresAt": res.ExpiresAt.Format(time.RFC3339), "user": toUserWithPerms(res.User),
	})
}

func (h handlers) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.Auth.Logout(r.Context(), bearerToken(r)); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h handlers) me(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	u, err := h.Auth.Me(r.Context(), p.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserWithPerms(u))
}

func (h handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.Auth.ChangePassword(r.Context(), p.UserID, in.Current, in.New); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := h.Auth.ListUsers(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapList(us, toUser))
}

func (h handlers) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string      `json:"name"`
		Email      string      `json:"email"`
		Password   string      `json:"password"`
		Role       domain.Role `json:"role"`
		TruckID    int64       `json:"truckId"`
		CustomerID int64       `json:"customerId"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	u, err := h.Auth.CreateUser(r.Context(), usecase.NewUser{Name: in.Name, Email: in.Email, Password: in.Password, Role: in.Role, TruckID: in.TruckID, CustomerID: in.CustomerID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toUser(u))
}

func (h handlers) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Name       string      `json:"name"`
		Role       domain.Role `json:"role"`
		Active     bool        `json:"active"`
		TruckID    int64       `json:"truckId"`
		CustomerID int64       `json:"customerId"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	u, err := h.Auth.UpdateUser(r.Context(), id, usecase.EditUser{Name: in.Name, Role: in.Role, Active: in.Active, TruckID: in.TruckID, CustomerID: in.CustomerID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUser(u))
}

func (h handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.Auth.ResetPassword(r.Context(), id, in.Password); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

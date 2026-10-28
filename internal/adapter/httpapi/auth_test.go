package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayorea/backend/internal/adapter/httpapi"
	"github.com/mayorea/backend/internal/adapter/memory"
	"github.com/mayorea/backend/internal/domain"
	platformauth "github.com/mayorea/backend/internal/platform/auth"
	"github.com/mayorea/backend/internal/platform/clock"
	"github.com/mayorea/backend/internal/usecase"
)

type app struct {
	t       *testing.T
	srv     http.Handler
	auth    *usecase.AuthService
	catalog *usecase.CatalogService
	trips   *usecase.TripService
}

func newApp(t *testing.T) *app {
	t.Helper()
	st := memory.NewStore()
	clk := &clock.Fixed{Day: time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)}
	authSvc, err := usecase.NewAuthService(st.Users(), st.Sessions(), st.Trucks(), platformauth.NewBcrypt(4), platformauth.Tokens{}, clk, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	orders := usecase.NewOrderService(st, st.Orders(), st.Customers(), st.Products(), st.Invoices(), clk)
	dispatch := usecase.NewDispatchService(st, st.Trucks(), st.Orders(), st.Customers(), st.Products(), st.Lots(), st.Invoices(), st.Rates(), clk)
	collections := usecase.NewCollectionService(st, st.Invoices(), st.Rates(), clk)
	catalog := usecase.NewCatalogService(st.Products(), st.Lots(), st.Customers(), st.Trucks(), st.Rates(), clk)
	trips := usecase.NewTripService(st, st.Trips(), st.Trucks(), st.Orders(), st.Customers(), st.Products(), st.Invoices(),
		st.Lots(), st.Rates(), dispatch, orders, collections, clk)
	svc := httpapi.Services{Catalog: catalog, Orders: orders, Dispatch: dispatch, Collections: collections, Trips: trips, Auth: authSvc}
	return &app{t: t, srv: httpapi.NewRouter(svc, httpapi.Options{}), auth: authSvc, catalog: catalog, trips: trips}
}

func (a *app) user(role domain.Role, email string, truckID int64) string {
	a.t.Helper()
	_, err := a.auth.CreateUser(context.Background(), usecase.NewUser{Name: email, Email: email, Password: "clave-segura-1", Role: role, TruckID: truckID})
	if err != nil {
		a.t.Fatal(err)
	}
	return a.login(email, "clave-segura-1")
}

func (a *app) login(email, password string) string {
	a.t.Helper()
	rec := a.do("POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": password})
	if rec.Code != http.StatusOK {
		a.t.Fatalf("login %s: %d %s", email, rec.Code, rec.Body)
	}
	var out struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Token
}

func (a *app) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.srv.ServeHTTP(rec, req)
	return rec
}

func (a *app) expect(want int, method, path, token string, body any) {
	a.t.Helper()
	if rec := a.do(method, path, token, body); rec.Code != want {
		a.t.Errorf("%s %s: got %d, want %d (%s)", method, path, rec.Code, want, rec.Body)
	}
}

func TestEveryRouteDemandsAuthentication(t *testing.T) {
	a := newApp(t)
	for _, r := range [][2]string{
		{"GET", "/api/v1/products"}, {"POST", "/api/v1/orders"}, {"GET", "/api/v1/users"},
		{"GET", "/api/v1/trips"}, {"GET", "/api/v1/invoices"}, {"PUT", "/api/v1/exchange-rate"},
	} {
		a.expect(http.StatusUnauthorized, r[0], r[1], "", nil)
	}
	a.expect(http.StatusUnauthorized, "GET", "/api/v1/products", "token-inventado", nil)
	a.expect(http.StatusOK, "GET", "/health", "", nil) // the only public route besides login
}

func TestRolePermissions(t *testing.T) {
	a := newApp(t)
	owner := a.user(domain.RoleOwner, "dueno@g.com", 0)
	credit := a.user(domain.RoleCredit, "credito@g.com", 0)
	warehouse := a.user(domain.RoleWarehouse, "galpon@g.com", 0)
	sales := a.user(domain.RoleSalesperson, "prev@g.com", 0)
	truck, _ := a.catalog.CreateTruck(context.Background(), usecase.NewTruck{Name: "C1"})
	driver := a.user(domain.RoleDriver, "chofer@g.com", truck.ID)

	product := map[string]any{"name": "Harina", "unit": "bulto", "priceUsd": 1000}
	rate := map[string]any{"rate": 365214}

	checks := []struct {
		who, method, path string
		token             string
		body              any
		want              int
	}{
		{"owner creates product", "POST", "/api/v1/products", owner, product, http.StatusCreated},
		{"warehouse creates product", "POST", "/api/v1/products", warehouse, map[string]any{"name": "Arroz", "unit": "bulto", "priceUsd": 900}, http.StatusCreated},
		{"salesperson cannot create product", "POST", "/api/v1/products", sales, product, http.StatusForbidden},
		{"driver cannot create product", "POST", "/api/v1/products", driver, product, http.StatusForbidden},
		{"salesperson reads catalog", "GET", "/api/v1/products", sales, nil, http.StatusOK},
		{"credit sets the rate", "PUT", "/api/v1/exchange-rate", credit, rate, http.StatusOK},
		{"warehouse cannot set the rate", "PUT", "/api/v1/exchange-rate", warehouse, rate, http.StatusForbidden},
		{"salesperson cannot approve", "POST", "/api/v1/orders/1/approve", sales, nil, http.StatusForbidden},
		{"warehouse cannot approve", "POST", "/api/v1/orders/1/approve", warehouse, nil, http.StatusForbidden},
		{"credit may approve (order missing)", "POST", "/api/v1/orders/1/approve", credit, nil, http.StatusNotFound},
		{"credit cannot dispatch", "POST", "/api/v1/trucks/1/dispatch", credit, nil, http.StatusForbidden},
		{"driver cannot dispatch", "POST", "/api/v1/trucks/1/dispatch", driver, nil, http.StatusForbidden},
		{"warehouse may dispatch (nothing to ship)", "POST", "/api/v1/trucks/1/dispatch", warehouse, nil, http.StatusUnprocessableEntity},
		{"driver cannot register office payments", "POST", "/api/v1/invoices/1/payments", driver, map[string]any{"cashUsd": 100}, http.StatusForbidden},
		{"salesperson cannot see invoices", "GET", "/api/v1/invoices", sales, nil, http.StatusForbidden},
		{"credit sees invoices", "GET", "/api/v1/invoices", credit, nil, http.StatusOK},
		{"only the owner manages users", "GET", "/api/v1/users", credit, nil, http.StatusForbidden},
		{"owner lists users", "GET", "/api/v1/users", owner, nil, http.StatusOK},
		{"driver cannot approve settlements", "POST", "/api/v1/trips/1/settlement/approve", driver, nil, http.StatusForbidden},
	}
	for _, c := range checks {
		if rec := a.do(c.method, c.path, c.token, c.body); rec.Code != c.want {
			t.Errorf("%s: %s %s = %d, want %d (%s)", c.who, c.method, c.path, rec.Code, c.want, rec.Body)
		}
	}
}

func TestDriverIsScopedToTheirOwnTruck(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	t1 := mustTruck(t, a, "C1")
	t2 := mustTruck(t, a, "C2")
	if _, err := a.catalog.SetRate(ctx, 365_214); err != nil {
		t.Fatal(err)
	}
	p, _ := a.catalog.CreateProduct(ctx, usecase.NewProduct{Name: "Harina", Unit: domain.UnitBulk, PriceUSD: 1000})
	if _, err := a.catalog.ReceiveLot(ctx, usecase.NewLot{ProductID: p.ID, Code: "L", ExpiresOn: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Quantity: 100}); err != nil {
		t.Fatal(err)
	}
	trip1, err := a.trips.Depart(ctx, t1.ID, []domain.Qty{{ProductID: p.ID, Quantity: 10}})
	if err != nil {
		t.Fatal(err)
	}
	trip2, err := a.trips.Depart(ctx, t2.ID, []domain.Qty{{ProductID: p.ID, Quantity: 10}})
	if err != nil {
		t.Fatal(err)
	}

	driver1 := a.user(domain.RoleDriver, "chofer1@g.com", t1.ID)
	own := "/api/v1/trips/" + itoa(trip1.ID)
	other := "/api/v1/trips/" + itoa(trip2.ID)

	a.expect(http.StatusOK, "GET", own, driver1, nil)
	a.expect(http.StatusForbidden, "GET", other, driver1, nil)
	a.expect(http.StatusForbidden, "POST", other+"/close", driver1, map[string]any{"cashUsd": 0})
	a.expect(http.StatusForbidden, "POST", other+"/sales", driver1, map[string]any{"customerId": 1, "terms": "contado"})

	var list []struct{ ID int64 }
	rec := a.do("GET", "/api/v1/trips?truckId="+itoa(t2.ID), driver1, nil) // asking for the other truck is overridden
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != trip1.ID {
		t.Fatalf("driver must only list their own trips, got %+v", list)
	}

	owner := a.user(domain.RoleOwner, "dueno@g.com", 0)
	a.expect(http.StatusOK, "GET", other, owner, nil)
}

func TestLoginLogoutAndMe(t *testing.T) {
	a := newApp(t)
	token := a.user(domain.RoleCredit, "credito@g.com", 0)

	rec := a.do("GET", "/api/v1/auth/me", token, nil)
	var me struct {
		Role        string
		Permissions []string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != 200 || me.Role != "credito" || len(me.Permissions) == 0 {
		t.Fatalf("me = %d %s", rec.Code, rec.Body)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("password")) {
		t.Fatal("the user payload must never expose password data")
	}

	a.expect(http.StatusUnauthorized, "POST", "/api/v1/auth/login", "", map[string]string{"email": "credito@g.com", "password": "mala-clave-1"})
	a.expect(http.StatusNoContent, "POST", "/api/v1/auth/logout", token, nil)
	a.expect(http.StatusUnauthorized, "GET", "/api/v1/auth/me", token, nil)
}

func mustTruck(t *testing.T, a *app, name string) domain.Truck {
	t.Helper()
	tr, err := a.catalog.CreateTruck(context.Background(), usecase.NewTruck{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

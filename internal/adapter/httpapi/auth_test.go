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
	authSvc, err := usecase.NewAuthService(st.Users(), st.Sessions(), st.Trucks(), st.Customers(), platformauth.NewBcrypt(4), platformauth.Tokens{}, clk, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	orders := usecase.NewOrderService(st, st.Orders(), st.Customers(), st.Products(), st.Invoices(), clk)
	dispatch := usecase.NewDispatchService(st, st.Trucks(), st.Orders(), st.Customers(), st.Products(), st.Lots(), st.Invoices(), st.Rates(), clk)
	collections := usecase.NewCollectionService(st, st.Invoices(), st.Rates(), clk)
	catalog := usecase.NewCatalogService(st.Products(), st.Lots(), st.Customers(), st.Trucks(), st.Rates(), clk)
	trips := usecase.NewTripService(st, st.Trips(), st.Trucks(), st.Orders(), st.Customers(), st.Products(), st.Invoices(),
		st.Lots(), st.Rates(), dispatch, orders, collections, clk)
	svc := httpapi.Services{Catalog: catalog, Orders: orders, Dispatch: dispatch, Collections: collections, Trips: trips, Auth: authSvc, Company: usecase.NewCompanyService(st.Company(), st.Customers())}
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

	// A driver may send out their own truck, never someone else's, and not just anyone can.
	a.expect(http.StatusForbidden, "POST", "/api/v1/trucks/"+itoa(t2.ID)+"/depart", driver1, map[string]any{})
	a.expect(http.StatusUnprocessableEntity, "POST", "/api/v1/trucks/"+itoa(t1.ID)+"/depart", driver1, map[string]any{}) // allowed; the truck is already out... nothing new to load
	a.expect(http.StatusForbidden, "POST", "/api/v1/trucks/"+itoa(t1.ID)+"/depart", a.user(domain.RoleCredit, "cred@g.com", 0), map[string]any{})

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

func TestCommerceSeesOnlyItsOwnAccount(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	if _, err := a.catalog.SetRate(ctx, 365_214); err != nil {
		t.Fatal(err)
	}
	p, _ := a.catalog.CreateProduct(ctx, usecase.NewProduct{Name: "Harina", Unit: domain.UnitBulk, PriceUSD: 1000})
	if _, err := a.catalog.ReceiveLot(ctx, usecase.NewLot{ProductID: p.ID, Code: "L", ExpiresOn: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Quantity: 100}); err != nil {
		t.Fatal(err)
	}
	mine, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Abasto Sol", RIF: "J-12345678-9", Route: "Norte", CreditLimitUSD: 100_000, CreditDays: 7})
	other, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Bodega Luna", RIF: "J-98765432-1", Route: "Sur", CreditLimitUSD: 100_000, CreditDays: 7})

	shop := a.shop("sol@g.com", mine.ID)
	owner := a.user(domain.RoleOwner, "dueno@g.com", 0)

	// Browsing: products and the rate are open; stock and trips are not (its own fleet has its own test).
	a.expect(http.StatusOK, "GET", "/api/v1/products", shop, nil)
	a.expect(http.StatusOK, "GET", "/api/v1/exchange-rate", shop, nil)
	for _, path := range []string{"/api/v1/lots", "/api/v1/trips"} {
		a.expect(http.StatusForbidden, "GET", path, shop, nil)
	}

	// The customer list holds only itself.
	var cs []struct{ ID int64 }
	if err := json.Unmarshal(a.do("GET", "/api/v1/customers", shop, nil).Body.Bytes(), &cs); err != nil || len(cs) != 1 || cs[0].ID != mine.ID {
		t.Fatalf("customers = %+v (%v)", cs, err)
	}
	a.expect(http.StatusOK, "GET", "/api/v1/customers/"+itoa(mine.ID)+"/credit", shop, nil)
	a.expect(http.StatusNotFound, "GET", "/api/v1/customers/"+itoa(other.ID)+"/credit", shop, nil)

	// An order is always for the caller, whatever customerId the body claims.
	body := map[string]any{"customerId": other.ID, "terms": "contado", "channel": "oficina", "lines": []map[string]any{{"productId": p.ID, "quantity": 2}}}
	rec := a.do("POST", "/api/v1/orders", shop, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create order: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		ID         int64
		CustomerID int64
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.CustomerID != mine.ID {
		t.Fatalf("order went to customer %d, want %d", created.CustomerID, mine.ID)
	}

	// Someone else's order is invisible; the owner's is not.
	body["customerId"] = other.ID
	ownerOrder := a.do("POST", "/api/v1/orders", owner, body)
	var theirs struct{ ID int64 }
	_ = json.Unmarshal(ownerOrder.Body.Bytes(), &theirs)
	a.expect(http.StatusOK, "GET", "/api/v1/orders/"+itoa(created.ID), shop, nil)
	a.expect(http.StatusNotFound, "GET", "/api/v1/orders/"+itoa(theirs.ID), shop, nil)
	var orders []struct{ CustomerID int64 }
	_ = json.Unmarshal(a.do("GET", "/api/v1/orders?customerId="+itoa(other.ID), shop, nil).Body.Bytes(), &orders)
	if len(orders) != 1 || orders[0].CustomerID != mine.ID {
		t.Fatalf("orders = %+v", orders)
	}

	// It cannot approve, review, collect or look at the office's books.
	a.expect(http.StatusForbidden, "POST", "/api/v1/orders/"+itoa(created.ID)+"/approve", shop, nil)
	a.expect(http.StatusForbidden, "POST", "/api/v1/invoices/1/payments", shop, map[string]any{"cashUsd": 100})
	a.expect(http.StatusForbidden, "POST", "/api/v1/customers", shop, map[string]any{"name": "X"})
	a.expect(http.StatusOK, "GET", "/api/v1/invoices", shop, nil)
}

func (a *app) shop(email string, customerID int64) string {
	a.t.Helper()
	_, err := a.auth.CreateUser(context.Background(), usecase.NewUser{Name: email, Email: email, Password: "clave-segura-1", Role: domain.RoleShop, CustomerID: customerID})
	if err != nil {
		a.t.Fatal(err)
	}
	return a.login(email, "clave-segura-1")
}

func TestCompanyProfile(t *testing.T) {
	a := newApp(t)
	owner := a.user(domain.RoleOwner, "dueno@g.com", 0)
	credit := a.user(domain.RoleCredit, "credito@g.com", 0)
	profile := map[string]any{
		"tradeName": " Distribuidora Sol ", "legalName": "Distribuidora Sol, C.A.", "rif": "j123456789",
		"phone": "0241-5550100", "email": "admin@sol.com", "address": "Zona Industrial", "city": "Valencia", "state": "Carabobo", "logo": "",
	}

	// A fresh install has a blank profile, readable by anyone signed in.
	rec := a.do("GET", "/api/v1/company", credit, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var blank map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &blank)
	if blank["tradeName"] != "" {
		t.Fatalf("expected a blank profile, got %v", blank)
	}
	a.expect(http.StatusUnauthorized, "GET", "/api/v1/company", "", nil)

	// Only owners write it; the data is validated and cleaned.
	a.expect(http.StatusForbidden, "PUT", "/api/v1/company", credit, profile)
	a.expect(http.StatusBadRequest, "PUT", "/api/v1/company", owner, map[string]any{"tradeName": "X", "legalName": "X", "rif": "malo"})
	a.expect(http.StatusBadRequest, "PUT", "/api/v1/company", owner, map[string]any{"tradeName": "X", "legalName": "X", "rif": "J-12345678-9", "logo": "javascript:alert(1)"})
	if rec := a.do("PUT", "/api/v1/company", owner, profile); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}

	rec = a.do("GET", "/api/v1/company", credit, nil)
	var saved map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &saved)
	if saved["tradeName"] != "Distribuidora Sol" || saved["rif"] != "J-12345678-9" {
		t.Fatalf("saved = %v", saved)
	}
}

func TestCommerceEditsOnlyItsOwnContactData(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	mine, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Abasto Sol", RIF: "J-12345678-9", Route: "Norte", Phone: "1", CreditLimitUSD: 100_000, CreditDays: 7})
	other, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Bodega Luna", RIF: "J-98765432-1", Route: "Sur", CreditLimitUSD: 50_000, CreditDays: 7})
	shop := a.shop("sol@g.com", mine.ID)
	credit := a.user(domain.RoleCredit, "credito@g.com", 0)

	// It may change its name, RIF and phone, but not its route or credit terms, even if it sends them.
	body := map[string]any{"name": "Abasto El Sol", "rif": "J-12345678-9", "phone": "0241-1", "route": "Sur", "creditLimitUsd": 999_999, "creditDays": 90}
	rec := a.do("PUT", "/api/v1/customers/"+itoa(mine.ID), shop, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Name           string
		Phone          string
		Route          string
		CreditLimitUsd int64
		CreditDays     int
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Name != "Abasto El Sol" || got.Phone != "0241-1" || got.Route != "Norte" || got.CreditLimitUsd != 100_000 || got.CreditDays != 7 {
		t.Fatalf("got %+v", got)
	}
	a.expect(http.StatusNotFound, "PUT", "/api/v1/customers/"+itoa(other.ID), shop, body)
	a.expect(http.StatusBadRequest, "PUT", "/api/v1/customers/"+itoa(mine.ID), shop, map[string]any{"name": "", "rif": "J-12345678-9"})

	// The office can change everything.
	a.expect(http.StatusOK, "PUT", "/api/v1/customers/"+itoa(other.ID), credit, map[string]any{"name": "Bodega Luna", "rif": "J-98765432-1", "route": "Este", "creditLimitUsd": 70_000, "creditDays": 15})
}

func TestCommerceKeepsItsOwnCompanyProfile(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	mine, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Abasto Sol", RIF: "J-12345678-9", Route: "Norte", Phone: "1", CreditLimitUSD: 100_000, CreditDays: 7})
	other, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Bodega Luna", RIF: "J-98765432-1", Route: "Sur"})
	shopA := a.shop("sol@g.com", mine.ID)
	shopB := a.shop("luna@g.com", other.ID)
	owner := a.user(domain.RoleOwner, "admin@g.com", 0)

	get := func(token string) map[string]string {
		var m map[string]string
		_ = json.Unmarshal(a.do("GET", "/api/v1/company", token, nil).Body.Bytes(), &m)
		return m
	}

	// Before filling anything in, a commerce sees what the office registered for it.
	if g := get(shopA); g["tradeName"] != "Abasto Sol" || g["rif"] != "J-12345678-9" {
		t.Fatalf("default = %v", g)
	}

	profile := map[string]any{"tradeName": "Abasto El Sol", "legalName": "Abasto El Sol, C.A.", "rif": "J-12345678-9", "phone": "0241-9", "email": "sol@abasto.com", "address": "Calle 1", "city": "Valencia", "state": "Carabobo", "logo": ""}
	a.expect(http.StatusOK, "PUT", "/api/v1/company", shopA, profile)

	// It lands in its own row: the commerce reads it back, the other commerce and the distributor do not see it.
	if g := get(shopA); g["tradeName"] != "Abasto El Sol" || g["address"] != "Calle 1" {
		t.Fatalf("shop A = %v", g)
	}
	if g := get(shopB); g["tradeName"] != "Bodega Luna" {
		t.Fatalf("shop B = %v", g)
	}
	if g := get(owner); g["tradeName"] != "" {
		t.Fatalf("distributor = %v", g)
	}

	// The customer record the office works with follows.
	var cs []struct{ Name, Phone string }
	_ = json.Unmarshal(a.do("GET", "/api/v1/customers", shopA, nil).Body.Bytes(), &cs)
	if len(cs) != 1 || cs[0].Name != "Abasto El Sol" || cs[0].Phone != "0241-9" {
		t.Fatalf("customer = %+v", cs)
	}

	// And the distributor can still keep its own.
	a.expect(http.StatusOK, "PUT", "/api/v1/company", owner, profile)
	if g := get(shopB); g["tradeName"] != "Bodega Luna" {
		t.Fatalf("shop B after the distributor saved = %v", g)
	}
}

func TestCommerceManagesItsOwnFleet(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	mine, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Abasto Sol", RIF: "J-12345678-9", Route: "Norte", CreditLimitUSD: 100_000, CreditDays: 7})
	other, _ := a.catalog.CreateCustomer(ctx, usecase.NewCustomer{Name: "Bodega Luna", RIF: "J-98765432-1", Route: "Sur", CreditLimitUSD: 100_000, CreditDays: 7})
	shop := a.shop("sol@g.com", mine.ID)
	luna := a.shop("luna@g.com", other.ID)
	owner := a.user(domain.RoleOwner, "dueno@g.com", 0)
	own, _ := a.catalog.CreateTruck(ctx, usecase.NewTruck{Name: "De la distribuidora"})

	// Creating a truck: the owner comes from the session, so the body cannot name one.
	a.expect(http.StatusBadRequest, "POST", "/api/v1/trucks", shop, map[string]any{"name": "Robado", "customerId": other.ID})
	rec := a.do("POST", "/api/v1/trucks", shop, map[string]any{"name": "Triton", "routes": []string{"Valencia"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("shop creates truck: %d %s", rec.Code, rec.Body)
	}
	var truck struct{ ID int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &truck)
	lunaTruck, _ := a.catalog.CreateTruck(ctx, usecase.NewTruck{Name: "De Luna", CustomerID: other.ID})

	// Each commerce lists only its own trucks; the owner sees them all.
	count := func(token string) int {
		var ts []struct{ ID int64 }
		_ = json.Unmarshal(a.do("GET", "/api/v1/trucks", token, nil).Body.Bytes(), &ts)
		return len(ts)
	}
	if n := count(shop); n != 1 {
		t.Fatalf("shop sees %d trucks, want 1", n)
	}
	if n := count(luna); n != 1 {
		t.Fatalf("luna sees %d trucks, want 1 (not the one the other commerce made)", n)
	}
	if n := count(owner); n != 3 {
		t.Fatalf("owner sees %d trucks, want 3", n)
	}

	// Drivers: only for its own trucks, and only the driver role.
	driver := func(email string, truckID int64) map[string]any {
		return map[string]any{"name": "Pedro", "email": email, "password": "clave-segura-1", "role": "chofer", "truckId": truckID}
	}
	a.expect(http.StatusCreated, "POST", "/api/v1/users", shop, driver("pedro@g.com", truck.ID))
	a.expect(http.StatusNotFound, "POST", "/api/v1/users", shop, driver("ajeno@g.com", lunaTruck.ID))
	a.expect(http.StatusNotFound, "POST", "/api/v1/users", shop, driver("dist@g.com", own.ID))
	a.expect(http.StatusForbidden, "POST", "/api/v1/users", shop, map[string]any{"name": "X", "email": "x@g.com", "password": "clave-segura-1", "role": "admin"})
	var us []struct{ Email string }
	_ = json.Unmarshal(a.do("GET", "/api/v1/users", shop, nil).Body.Bytes(), &us)
	if len(us) != 1 || us[0].Email != "pedro@g.com" {
		t.Fatalf("shop lists %+v, want only its own driver", us)
	}

	// It still cannot edit users or reach the office's dispatch.
	a.expect(http.StatusForbidden, "PATCH", "/api/v1/users/1", shop, map[string]any{"name": "X", "role": "chofer", "active": true, "truckId": truck.ID})
	a.expect(http.StatusForbidden, "POST", "/api/v1/trucks/"+itoa(truck.ID)+"/dispatch", shop, nil)
}

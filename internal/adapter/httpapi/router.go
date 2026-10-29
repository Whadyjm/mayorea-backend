// Package httpapi is the HTTP/JSON delivery adapter.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

// Pinger lets /health report on the storage backend.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Options struct {
	AllowedOrigins []string
	Health         Pinger // optional
}

func NewRouter(svc Services, opt Options) http.Handler {
	h := handlers{svc}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if opt.Health != nil {
			if err := opt.Health.Ping(r.Context()); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, errorBody{"database unavailable"})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	const v = "/api/v1"
	// route registers a handler behind authentication and one permission.
	// An empty permission only requires a valid session.
	route := func(pattern string, perm domain.Permission, fn http.HandlerFunc) {
		method, path, _ := cutSpace(pattern)
		mux.HandleFunc(method+" "+v+path, h.require(perm, fn))
	}

	// Public.
	mux.HandleFunc("POST "+v+"/auth/login", h.login)

	// Any signed-in user.
	route("POST /auth/logout", "", h.logout)
	route("GET /auth/me", "", h.me)
	route("POST /auth/password", "", h.changePassword)

	// Users.
	route("GET /users", domain.PermUsersManage, h.listUsers)
	route("POST /users", domain.PermUsersManage, h.createUser)
	route("PATCH /users/{id}", domain.PermUsersManage, h.updateUser)
	route("POST /users/{id}/password", domain.PermUsersManage, h.resetPassword)

	// Company profile: anyone signed in may read it, only owners change it.
	route("GET /company", "", h.getCompany)
	route("PUT /company", domain.PermCompanyWrite, h.saveCompany)

	// Catalog.
	route("GET /exchange-rate", domain.PermCatalogBrowse, h.getRate)
	route("PUT /exchange-rate", domain.PermRateWrite, h.setRate)
	route("GET /products", domain.PermCatalogBrowse, h.listProducts)
	route("POST /products", domain.PermCatalogWrite, h.createProduct)
	route("GET /lots", domain.PermCatalogRead, h.listStockedLots)
	route("GET /products/{id}/lots", domain.PermCatalogRead, h.listLots)
	route("POST /products/{id}/lots", domain.PermCatalogWrite, h.receiveLot)
	route("GET /customers", domain.PermCatalogBrowse, h.listCustomers)
	route("POST /customers", domain.PermCustomersWrite, h.createCustomer)
	route("PUT /customers/{id}", domain.PermCustomersEdit, h.updateCustomer)
	route("GET /customers/{id}/credit", domain.PermOrdersRead, h.customerCredit)
	route("GET /trucks", domain.PermCatalogRead, h.listTrucks)
	route("POST /trucks", domain.PermCatalogWrite, h.createTruck)

	// Orders.
	route("GET /orders", domain.PermOrdersRead, h.listOrders)
	route("POST /orders", domain.PermOrdersCreate, h.createOrder)
	route("GET /orders/{id}", domain.PermOrdersRead, h.getOrder)
	route("GET /orders/{id}/review", domain.PermOrdersApprove, h.reviewOrder)
	route("POST /orders/{id}/approve", domain.PermOrdersApprove, h.transition(func(s *usecase.OrderService, r *http.Request, id int64) (domain.Order, error) {
		return s.Approve(r.Context(), id)
	}))
	route("POST /orders/{id}/reject", domain.PermOrdersApprove, h.transition(func(s *usecase.OrderService, r *http.Request, id int64) (domain.Order, error) {
		return s.Reject(r.Context(), id)
	}))

	// Dispatch.
	route("GET /trucks/{id}/load-plan", domain.PermDispatchManage, h.loadPlan)
	route("POST /trucks/{id}/dispatch", domain.PermDispatchManage, h.dispatch)
	route("POST /trucks/{id}/depart", domain.PermTripsDepart, h.ownTruck(h.depart))

	// Trips: drivers act only on their own truck's trip.
	route("GET /trips", domain.PermTripsRead, h.listTrips)
	route("GET /trips/{id}", domain.PermTripsRead, h.ownTrip(h.getTrip))
	route("POST /trips/{id}/deliveries", domain.PermTripsOperate, h.ownTrip(h.deliver))
	route("POST /trips/{id}/sales", domain.PermTripsOperate, h.ownTrip(h.routeSale))
	route("POST /trips/{id}/presales", domain.PermTripsOperate, h.ownTrip(h.presale))
	route("POST /trips/{id}/payments", domain.PermTripsOperate, h.ownTrip(h.routeCollect))
	route("POST /trips/{id}/close", domain.PermTripsOperate, h.ownTrip(h.closeTrip))
	route("POST /trips/{id}/settlement/approve", domain.PermTripsSettle, h.approveSettlement)

	// Receivables.
	route("GET /invoices", domain.PermCollectionsRead, h.listInvoices)
	route("GET /invoices/{id}", domain.PermCollectionsRead, h.getInvoice)
	route("POST /invoices/{id}/payments/preview", domain.PermCollectionsWrite, h.previewPayment)
	route("POST /invoices/{id}/payments", domain.PermCollectionsWrite, h.registerPayment)

	return chain(mux, recoverer, requestLog, cors(opt.AllowedOrigins))
}

// cutSpace splits "METHOD /path" into its two parts.
func cutSpace(s string) (before, after string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

type middleware func(http.Handler) http.Handler

// chain wraps h so the first middleware is the outermost.
func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.ErrorContext(r.Context(), "panic", "panic", rec, "path", r.URL.Path)
				writeJSON(w, http.StatusInternalServerError, errorBody{"Algo salió mal de nuestro lado. Intenta de nuevo."})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// requestLog logs method, path and status. It never logs headers or bodies,
// so tokens and passwords stay out of the logs.
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.InfoContext(r.Context(), "http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", time.Since(start).Milliseconds())
	})
}

func cors(origins []string) middleware {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if o := r.Header.Get("Origin"); o != "" && allowed[o] {
				w.Header().Set("Access-Control-Allow-Origin", o)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

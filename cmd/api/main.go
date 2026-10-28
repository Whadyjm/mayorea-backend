// Command api is the composition root: it picks the adapters and wires the
// use cases to them. No business logic lives here.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mayorea/backend/internal/adapter/httpapi"
	"github.com/mayorea/backend/internal/adapter/memory"
	"github.com/mayorea/backend/internal/adapter/postgres"
	platformauth "github.com/mayorea/backend/internal/platform/auth"
	"github.com/mayorea/backend/internal/platform/clock"
	"github.com/mayorea/backend/internal/platform/config"
	"github.com/mayorea/backend/internal/usecase"
	"github.com/mayorea/backend/migrations"
)

// storage is what each persistence adapter offers to the composition root.
type storage struct {
	usecase.TxManager
	products  usecase.ProductRepository
	lots      usecase.LotRepository
	customers usecase.CustomerRepository
	orders    usecase.OrderRepository
	invoices  usecase.InvoiceRepository
	trucks    usecase.TruckRepository
	trips     usecase.TripRepository
	users     usecase.UserRepository
	sessions  usecase.SessionRepository
	rates     usecase.RateRepository
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		st     storage
		health httpapi.Pinger
	)
	switch cfg.Storage {
	case "postgres":
		db, err := postgres.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := db.Migrate(ctx, migrations.FS); err != nil {
			return err
		}
		st = storage{db, db.Products(), db.Lots(), db.Customers(), db.Orders(), db.Invoices(), db.Trucks(), db.Trips(), db.Users(), db.Sessions(), db.Rates()}
		health = db
	default:
		m := memory.NewStore()
		st = storage{m, m.Products(), m.Lots(), m.Customers(), m.Orders(), m.Invoices(), m.Trucks(), m.Trips(), m.Users(), m.Sessions(), m.Rates()}
		slog.Warn("using in-memory storage: data is lost on restart")
	}

	clk := clock.NewSystem(cfg.Timezone)
	authSvc, err := usecase.NewAuthService(st.users, st.sessions, st.trucks, platformauth.NewBcrypt(0), platformauth.Tokens{}, clk, cfg.SessionTTL)
	if err != nil {
		return err
	}
	if cfg.BootstrapEmail != "" {
		created, err := authSvc.Bootstrap(ctx, cfg.BootstrapName, cfg.BootstrapEmail, cfg.BootstrapPassword)
		if err != nil {
			return fmt.Errorf("bootstrap owner: %w", err)
		}
		if created {
			slog.Info("first owner created", "email", cfg.BootstrapEmail)
		}
	} else {
		slog.Warn("BOOTSTRAP_OWNER_EMAIL not set: if there are no users yet nobody will be able to sign in")
	}

	orders := usecase.NewOrderService(st, st.orders, st.customers, st.products, st.invoices, clk)
	dispatch := usecase.NewDispatchService(st, st.trucks, st.orders, st.customers, st.products, st.lots, st.invoices, st.rates, clk)
	collections := usecase.NewCollectionService(st, st.invoices, st.rates, clk)
	svc := httpapi.Services{
		Auth:        authSvc,
		Catalog:     usecase.NewCatalogService(st.products, st.lots, st.customers, st.trucks, st.rates, clk),
		Orders:      orders,
		Dispatch:    dispatch,
		Collections: collections,
		Trips: usecase.NewTripService(st, st.trips, st.trucks, st.orders, st.customers, st.products, st.invoices,
			st.lots, st.rates, dispatch, orders, collections, clk),
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.NewRouter(svc, httpapi.Options{AllowedOrigins: cfg.AllowedOrigins, Health: health}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "storage", cfg.Storage)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
	return nil
}

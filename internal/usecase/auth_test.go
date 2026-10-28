package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/mayorea/backend/internal/adapter/memory"
	"github.com/mayorea/backend/internal/domain"
	platformauth "github.com/mayorea/backend/internal/platform/auth"
	"github.com/mayorea/backend/internal/platform/clock"
	"github.com/mayorea/backend/internal/usecase"
)

const bcryptMinCost = 4 // keep tests fast

func newAuth(t *testing.T) (*usecase.AuthService, *clock.Fixed, *memory.Store) {
	t.Helper()
	st := memory.NewStore()
	clk := &clock.Fixed{Day: time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)}
	svc, err := usecase.NewAuthService(st.Users(), st.Sessions(), st.Trucks(), platformauth.NewBcrypt(bcryptMinCost), platformauth.Tokens{}, clk, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return svc, clk, st
}

func newOwner(t *testing.T, svc *usecase.AuthService) domain.User {
	t.Helper()
	return must(t, svc.CreateUser(context.Background(), usecase.NewUser{
		Name: "Ana", Email: "Ana@Galpon.com", Password: "clave-segura-1", Role: domain.RoleOwner,
	}))
}

func TestLoginAuthenticateLogout(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)
	if owner.Email != "ana@galpon.com" {
		t.Fatalf("email should be normalized, got %q", owner.Email)
	}

	res := must(t, svc.Login(ctx, " ANA@galpon.com ", "clave-segura-1"))
	p := must(t, svc.Authenticate(ctx, res.Token))
	if p.UserID != owner.ID || p.Role != domain.RoleOwner {
		t.Fatalf("principal = %+v", p)
	}

	if err := svc.Logout(ctx, res.Token); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Authenticate(ctx, res.Token)
	wantKind(t, err, domain.KindUnauthenticated)
}

func TestBadCredentialsAreIndistinguishable(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	newOwner(t, svc)

	_, wrongPass := svc.Login(ctx, "ana@galpon.com", "otra-clave-123")
	_, unknown := svc.Login(ctx, "nadie@galpon.com", "clave-segura-1")
	wantKind(t, wrongPass, domain.KindUnauthenticated)
	wantKind(t, unknown, domain.KindUnauthenticated)
	if wrongPass.Error() != unknown.Error() {
		t.Fatalf("messages differ and leak which emails exist: %q vs %q", wrongPass, unknown)
	}
}

func TestLoginLockoutAfterRepeatedFailures(t *testing.T) {
	ctx := context.Background()
	svc, clk, _ := newAuth(t)
	newOwner(t, svc)

	for i := 0; i < 5; i++ {
		_, err := svc.Login(ctx, "ana@galpon.com", "mal-mal-mal")
		wantKind(t, err, domain.KindUnauthenticated)
	}
	_, err := svc.Login(ctx, "ana@galpon.com", "clave-segura-1") // right password, but locked
	wantKind(t, err, domain.KindThrottled)

	clk.Day = clk.Day.Add(16 * time.Minute)
	must(t, svc.Login(ctx, "ana@galpon.com", "clave-segura-1"))
}

func TestSessionExpires(t *testing.T) {
	ctx := context.Background()
	svc, clk, _ := newAuth(t)
	newOwner(t, svc)
	res := must(t, svc.Login(ctx, "ana@galpon.com", "clave-segura-1"))

	clk.Day = clk.Day.Add(23 * time.Hour)
	must(t, svc.Authenticate(ctx, res.Token))
	clk.Day = clk.Day.Add(2 * time.Hour)
	_, err := svc.Authenticate(ctx, res.Token)
	wantKind(t, err, domain.KindUnauthenticated)
}

func TestDeactivatingAUserEndsTheirSessions(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)
	clerk := must(t, svc.CreateUser(ctx, usecase.NewUser{Name: "Luis", Email: "luis@galpon.com", Password: "clave-segura-2", Role: domain.RoleCredit}))
	res := must(t, svc.Login(ctx, "luis@galpon.com", "clave-segura-2"))

	must(t, svc.UpdateUser(ctx, clerk.ID, usecase.EditUser{Name: "Luis", Role: domain.RoleCredit, Active: false}))

	_, err := svc.Authenticate(ctx, res.Token)
	wantKind(t, err, domain.KindUnauthenticated)
	_, err = svc.Login(ctx, "luis@galpon.com", "clave-segura-2")
	wantKind(t, err, domain.KindUnauthenticated)
	_ = owner
}

func TestRoleChangeAppliesImmediately(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	newOwner(t, svc)
	u := must(t, svc.CreateUser(ctx, usecase.NewUser{Name: "Luis", Email: "luis@galpon.com", Password: "clave-segura-2", Role: domain.RoleCredit}))
	res := must(t, svc.Login(ctx, "luis@galpon.com", "clave-segura-2"))

	must(t, svc.UpdateUser(ctx, u.ID, usecase.EditUser{Name: "Luis", Role: domain.RoleSalesperson, Active: true}))

	p := must(t, svc.Authenticate(ctx, res.Token))
	if p.Role != domain.RoleSalesperson || p.Can(domain.PermOrdersApprove) {
		t.Fatalf("old privileges survived the role change: %+v", p)
	}
}

func TestCannotRemoveTheLastOwner(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)

	_, err := svc.UpdateUser(ctx, owner.ID, usecase.EditUser{Name: "Ana", Role: domain.RoleCredit, Active: true})
	wantKind(t, err, domain.KindRule)
	_, err = svc.UpdateUser(ctx, owner.ID, usecase.EditUser{Name: "Ana", Role: domain.RoleOwner, Active: false})
	wantKind(t, err, domain.KindRule)

	// With a second owner, the first can step down.
	must(t, svc.CreateUser(ctx, usecase.NewUser{Name: "Beto", Email: "beto@galpon.com", Password: "clave-segura-3", Role: domain.RoleOwner}))
	must(t, svc.UpdateUser(ctx, owner.ID, usecase.EditUser{Name: "Ana", Role: domain.RoleCredit, Active: true}))
}

func TestChangePasswordClosesSessions(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)
	res := must(t, svc.Login(ctx, "ana@galpon.com", "clave-segura-1"))

	wantKind(t, svc.ChangePassword(ctx, owner.ID, "no-es-esta-1", "nueva-clave-99"), domain.KindInvalid)
	if err := svc.ChangePassword(ctx, owner.ID, "clave-segura-1", "nueva-clave-99"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Authenticate(ctx, res.Token)
	wantKind(t, err, domain.KindUnauthenticated)
	must(t, svc.Login(ctx, "ana@galpon.com", "nueva-clave-99"))
}

func TestUserValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, st := newAuth(t)
	newOwner(t, svc)
	cat := usecase.NewCatalogService(st.Products(), st.Lots(), st.Customers(), st.Trucks(), st.Rates(), clock.Fixed{})
	truck := must(t, cat.CreateTruck(ctx, usecase.NewTruck{Name: "Camión 1"}))

	cases := map[string]usecase.NewUser{
		"short password":      {Name: "X", Email: "x@g.com", Password: "corta", Role: domain.RoleCredit},
		"bad email":           {Name: "X", Email: "x-g.com", Password: "clave-segura-1", Role: domain.RoleCredit},
		"driver without truck": {Name: "X", Email: "x@g.com", Password: "clave-segura-1", Role: domain.RoleDriver},
		"truck on non-driver": {Name: "X", Email: "x@g.com", Password: "clave-segura-1", Role: domain.RoleCredit, TruckID: truck.ID},
		"unknown role":        {Name: "X", Email: "x@g.com", Password: "clave-segura-1", Role: "rey"},
	}
	for name, in := range cases {
		if _, err := svc.CreateUser(ctx, in); domain.KindOf(err) != domain.KindInvalid {
			t.Errorf("%s: want invalid, got %v", name, err)
		}
	}
	_, err := svc.CreateUser(ctx, usecase.NewUser{Name: "Otra Ana", Email: "ANA@galpon.com", Password: "clave-segura-1", Role: domain.RoleCredit})
	wantKind(t, err, domain.KindConflict)
	must(t, svc.CreateUser(ctx, usecase.NewUser{Name: "Pedro", Email: "pedro@g.com", Password: "clave-segura-1", Role: domain.RoleDriver, TruckID: truck.ID}))
}

func TestBootstrapOnlyWhenEmpty(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	if created, err := svc.Bootstrap(ctx, "Dueño", "dueno@g.com", "clave-segura-1"); err != nil || !created {
		t.Fatalf("first bootstrap: created=%v err=%v", created, err)
	}
	if created, err := svc.Bootstrap(ctx, "Otro", "otro@g.com", "clave-segura-1"); err != nil || created {
		t.Fatalf("second bootstrap must be a no-op: created=%v err=%v", created, err)
	}
}

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
	svc, err := usecase.NewAuthService(st.Users(), st.Sessions(), st.Trucks(), st.Customers(), platformauth.NewBcrypt(bcryptMinCost), platformauth.Tokens{}, clk, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return svc, clk, st
}

func newOwner(t *testing.T, svc *usecase.AuthService) domain.User {
	t.Helper()
	return must(svc.CreateUser(context.Background(), usecase.NewUser{
		Name: "Ana", Email: "Ana@Galpon.com", Password: "clave-segura-1", Role: domain.RoleOwner,
	}))
}

func TestLoginAuthenticateLogout(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)
	if owner.Email != "ana@galpon.com" {
		t.Fatalf("email should be normalized, got %q", owner.Email)
	}

	res := must(svc.Login(ctx, " ANA@galpon.com ", "clave-segura-1"))
	p := must(svc.Authenticate(ctx, res.Token))
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
	defer catch(t)
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
	defer catch(t)
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
	must(svc.Login(ctx, "ana@galpon.com", "clave-segura-1"))
}

func TestSessionExpires(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, clk, _ := newAuth(t)
	newOwner(t, svc)
	res := must(svc.Login(ctx, "ana@galpon.com", "clave-segura-1"))

	clk.Day = clk.Day.Add(23 * time.Hour)
	must(svc.Authenticate(ctx, res.Token))
	clk.Day = clk.Day.Add(2 * time.Hour)
	_, err := svc.Authenticate(ctx, res.Token)
	wantKind(t, err, domain.KindUnauthenticated)
}

func TestDeactivatingAUserEndsTheirSessions(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)
	clerk := must(svc.CreateUser(ctx, usecase.NewUser{Name: "Luis", Email: "luis@galpon.com", Password: "clave-segura-2", Role: domain.RoleCredit}))
	res := must(svc.Login(ctx, "luis@galpon.com", "clave-segura-2"))

	must(svc.UpdateUser(ctx, clerk.ID, usecase.EditUser{Name: "Luis", Role: domain.RoleCredit, Active: false}))

	_, err := svc.Authenticate(ctx, res.Token)
	wantKind(t, err, domain.KindUnauthenticated)
	_, err = svc.Login(ctx, "luis@galpon.com", "clave-segura-2")
	wantKind(t, err, domain.KindUnauthenticated)
	_ = owner
}

func TestRoleChangeAppliesImmediately(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	newOwner(t, svc)
	u := must(svc.CreateUser(ctx, usecase.NewUser{Name: "Luis", Email: "luis@galpon.com", Password: "clave-segura-2", Role: domain.RoleCredit}))
	res := must(svc.Login(ctx, "luis@galpon.com", "clave-segura-2"))

	must(svc.UpdateUser(ctx, u.ID, usecase.EditUser{Name: "Luis", Role: domain.RoleSalesperson, Active: true}))

	p := must(svc.Authenticate(ctx, res.Token))
	if p.Role != domain.RoleSalesperson || p.Can(domain.PermOrdersApprove) {
		t.Fatalf("old privileges survived the role change: %+v", p)
	}
}

func TestCannotRemoveTheLastOwner(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)

	_, err := svc.UpdateUser(ctx, owner.ID, usecase.EditUser{Name: "Ana", Role: domain.RoleCredit, Active: true})
	wantKind(t, err, domain.KindRule)
	_, err = svc.UpdateUser(ctx, owner.ID, usecase.EditUser{Name: "Ana", Role: domain.RoleOwner, Active: false})
	wantKind(t, err, domain.KindRule)

	// With a second owner, the first can step down.
	must(svc.CreateUser(ctx, usecase.NewUser{Name: "Beto", Email: "beto@galpon.com", Password: "clave-segura-3", Role: domain.RoleOwner}))
	must(svc.UpdateUser(ctx, owner.ID, usecase.EditUser{Name: "Ana", Role: domain.RoleCredit, Active: true}))
}

func TestChangePasswordClosesSessions(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	owner := newOwner(t, svc)
	res := must(svc.Login(ctx, "ana@galpon.com", "clave-segura-1"))

	wantKind(t, svc.ChangePassword(ctx, owner.ID, "no-es-esta-1", "nueva-clave-99"), domain.KindInvalid)
	if err := svc.ChangePassword(ctx, owner.ID, "clave-segura-1", "nueva-clave-99"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Authenticate(ctx, res.Token)
	wantKind(t, err, domain.KindUnauthenticated)
	must(svc.Login(ctx, "ana@galpon.com", "nueva-clave-99"))
}

func TestUserValidation(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, st := newAuth(t)
	newOwner(t, svc)
	cat := usecase.NewCatalogService(st.Products(), st.Lots(), st.Customers(), st.Trucks(), st.Rates(), clock.Fixed{})
	truck := must(cat.CreateTruck(ctx, usecase.NewTruck{Name: "Camión 1"}))

	cases := map[string]usecase.NewUser{
		"short password":       {Name: "X", Email: "x@g.com", Password: "corta", Role: domain.RoleCredit},
		"bad email":            {Name: "X", Email: "x-g.com", Password: "clave-segura-1", Role: domain.RoleCredit},
		"driver without truck": {Name: "X", Email: "x@g.com", Password: "clave-segura-1", Role: domain.RoleDriver},
		"truck on non-driver":  {Name: "X", Email: "x@g.com", Password: "clave-segura-1", Role: domain.RoleCredit, TruckID: truck.ID},
		"unknown role":         {Name: "X", Email: "x@g.com", Password: "clave-segura-1", Role: "rey"},
	}
	for name, in := range cases {
		if _, err := svc.CreateUser(ctx, in); domain.KindOf(err) != domain.KindInvalid {
			t.Errorf("%s: want invalid, got %v", name, err)
		}
	}
	_, err := svc.CreateUser(ctx, usecase.NewUser{Name: "Otra Ana", Email: "ANA@galpon.com", Password: "clave-segura-1", Role: domain.RoleCredit})
	wantKind(t, err, domain.KindConflict)
	must(svc.CreateUser(ctx, usecase.NewUser{Name: "Pedro", Email: "pedro@g.com", Password: "clave-segura-1", Role: domain.RoleDriver, TruckID: truck.ID}))
}

func TestEnsureOwnersIsIdempotentAndNeverResetsPasswords(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, _ := newAuth(t)
	seeds := []usecase.SeedOwner{
		{Name: "Uno", Email: "uno@g.com", Password: "clave-segura-1"},
		{Name: "Dos", Email: "DOS@g.com", Password: "clave-segura-2"},
	}
	created := must(svc.EnsureOwners(ctx, seeds))
	if len(created) != 2 || created[1] != "dos@g.com" {
		t.Fatalf("created = %v", created)
	}
	for _, s := range seeds {
		if res := must(svc.Login(ctx, s.Email, s.Password)); res.User.Role != domain.RoleOwner {
			t.Fatalf("%s is not an owner", s.Email)
		}
	}

	// Second start, with a different password in the config: nothing changes.
	seeds[0].Password = "otra-clave-999"
	if again := must(svc.EnsureOwners(ctx, seeds)); len(again) != 0 {
		t.Fatalf("second run created %v", again)
	}
	must(svc.Login(ctx, "uno@g.com", "clave-segura-1"))
	_, err := svc.Login(ctx, "uno@g.com", "otra-clave-999")
	wantKind(t, err, domain.KindUnauthenticated)
}

func TestAllowlistLimitsWhoCanBeOwner(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, _ := newAuth(t)

	// Accounts that exist before the restriction is switched on.
	must(svc.CreateUser(ctx, usecase.NewUser{Name: "Fundador", Email: "fundador@g.com", Password: "clave-segura-1", Role: domain.RoleOwner}))
	must(svc.CreateUser(ctx, usecase.NewUser{Name: "Intruso", Email: "intruso@g.com", Password: "clave-segura-2", Role: domain.RoleOwner}))
	founder := must(svc.Login(ctx, "fundador@g.com", "clave-segura-1"))
	intruder := must(svc.Login(ctx, "intruso@g.com", "clave-segura-2"))

	svc.RestrictTo([]string{" Fundador@G.com "}) // case and spaces do not matter

	// An open session of someone outside the list dies at the next request.
	_, err := svc.Authenticate(ctx, intruder.Token)
	wantKind(t, err, domain.KindUnauthenticated)
	must(svc.Authenticate(ctx, founder.Token))

	// They cannot sign in again, even with the right password, and the answer
	// is the same as for a wrong password (it does not reveal the account exists).
	_, outside := svc.Login(ctx, "intruso@g.com", "clave-segura-2")
	_, wrong := svc.Login(ctx, "fundador@g.com", "mala-clave-123")
	wantKind(t, outside, domain.KindUnauthenticated)
	if outside.Error() != wrong.Error() {
		t.Fatalf("messages differ: %q vs %q", outside, wrong)
	}
	must(svc.Login(ctx, "FUNDADOR@g.com", "clave-segura-1"))

	// Only owners are gated: no new owner outside the list, and nobody can be
	// promoted into one, but the owners can create other roles freely.
	_, err = svc.CreateUser(ctx, usecase.NewUser{Name: "Otro", Email: "otro@g.com", Password: "clave-segura-3", Role: domain.RoleOwner})
	wantKind(t, err, domain.KindRule)
	clerk := must(svc.CreateUser(ctx, usecase.NewUser{Name: "Ana", Email: "ana@g.com", Password: "clave-segura-3", Role: domain.RoleCredit}))
	must(svc.Login(ctx, "ana@g.com", "clave-segura-3"))
	_, err = svc.UpdateUser(ctx, clerk.ID, usecase.EditUser{Name: "Ana", Role: domain.RoleOwner, Active: true})
	wantKind(t, err, domain.KindRule)

	// Lifting the restriction restores access.
	svc.RestrictTo(nil)
	must(svc.Login(ctx, "intruso@g.com", "clave-segura-2"))
}

func TestSeedingAnOwnerOutsideTheAllowlistFailsLoudly(t *testing.T) {
	defer catch(t)
	svc, _, _ := newAuth(t)
	svc.RestrictTo([]string{"fundador@g.com"})
	_, err := svc.EnsureOwners(context.Background(), []usecase.SeedOwner{{Name: "X", Email: "x@g.com", Password: "clave-segura-1"}})
	wantKind(t, err, domain.KindRule)
}

func TestCommerceAccountIsLinkedToOneCustomer(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	svc, _, st := newAuth(t)
	c := &domain.Customer{Name: "Abasto Sol", RIF: "J-12345678-9", Route: "Norte"}
	if err := st.Customers().Create(ctx, c); err != nil {
		t.Fatal(err)
	}

	// A commerce account needs its customer; other roles must not carry one.
	_, err := svc.CreateUser(ctx, usecase.NewUser{Name: "Abasto Sol", Email: "sol@g.com", Password: "clave-segura-1", Role: domain.RoleShop})
	wantKind(t, err, domain.KindInvalid)
	_, err = svc.CreateUser(ctx, usecase.NewUser{Name: "X", Email: "x@g.com", Password: "clave-segura-1", Role: domain.RoleCredit, CustomerID: c.ID})
	wantKind(t, err, domain.KindInvalid)
	_, err = svc.CreateUser(ctx, usecase.NewUser{Name: "Abasto Sol", Email: "sol@g.com", Password: "clave-segura-1", Role: domain.RoleShop, CustomerID: 999})
	wantKind(t, err, domain.KindNotFound)

	u := must(svc.CreateUser(ctx, usecase.NewUser{Name: "Abasto Sol", Email: "sol@g.com", Password: "clave-segura-1", Role: domain.RoleShop, CustomerID: c.ID}))
	if u.CustomerID != c.ID {
		t.Fatalf("customer id = %d", u.CustomerID)
	}
	res := must(svc.Login(ctx, "sol@g.com", "clave-segura-1"))
	if res.User.CustomerID != c.ID {
		t.Fatal("login lost the customer link")
	}

	// One login per commerce.
	_, err = svc.CreateUser(ctx, usecase.NewUser{Name: "Otro", Email: "otro@g.com", Password: "clave-segura-1", Role: domain.RoleShop, CustomerID: c.ID})
	wantKind(t, err, domain.KindConflict)
}

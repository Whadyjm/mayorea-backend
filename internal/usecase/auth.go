package usecase

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mayorea/backend/internal/domain"
)

const (
	maxLoginFailures = 5
	loginLockout     = 15 * time.Minute
)

// AuthService owns identity: login/logout, session checks and user management.
// The failed-login counter lives in process memory, which is enough for a
// single instance; behind a load balancer it should move to shared storage.
type AuthService struct {
	users     UserRepository
	sessions  SessionRepository
	trucks    TruckRepository
	customers CustomerRepository
	hasher    PasswordHasher
	tokens    Tokens
	clock     Clock
	ttl       time.Duration

	// allowed, when not nil, is the only set of emails that may exist as
	// accounts, sign in, or keep a session. See RestrictTo.
	allowed map[string]struct{}

	// dummyHash is verified when the email is unknown, so response time does
	// not reveal which emails exist.
	dummyHash string

	mu       sync.Mutex
	attempts map[string]loginAttempt
}

type loginAttempt struct {
	fails       int
	lockedUntil time.Time
}

func NewAuthService(u UserRepository, s SessionRepository, t TruckRepository, c CustomerRepository, h PasswordHasher, tk Tokens, clock Clock, ttl time.Duration) (*AuthService, error) {
	dummy, err := h.Hash("mayorea-dummy-password")
	if err != nil {
		return nil, err
	}
	return &AuthService{users: u, sessions: s, trucks: t, customers: c, hasher: h, tokens: tk, clock: clock, ttl: ttl,
		dummyHash: dummy, attempts: map[string]loginAttempt{}}, nil
}

var errBadCredentials = domain.Unauthenticated("Correo o contraseña incorrectos.")

type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	User      domain.User
}

func (s *AuthService) Login(ctx context.Context, email, password string) (LoginResult, error) {
	email = domain.NormalizeEmail(email)
	now := s.clock.Now()
	if err := s.checkLock(email, now); err != nil {
		return LoginResult{}, err
	}
	u, found, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		return LoginResult{}, err
	}
	// Someone outside the allowlist gets exactly the answer of an unknown email.
	if found && !s.permits(email, u.Role) {
		found = false
	}
	hash := s.dummyHash
	if found {
		hash = u.PasswordHash
	}
	// Always verify, even for unknown emails, to keep timing uniform.
	if ok := s.hasher.Verify(hash, password); !found || !ok || !u.Active {
		s.recordFailure(email, now)
		return LoginResult{}, errBadCredentials
	}
	s.clearFailures(email)

	token, digest, err := s.tokens.New()
	if err != nil {
		return LoginResult{}, err
	}
	_ = s.sessions.DeleteExpired(ctx, now) // housekeeping; a failure must not block login
	exp := now.Add(s.ttl)
	if err := s.sessions.Create(ctx, domain.Session{Digest: digest, UserID: u.ID, ExpiresAt: exp}); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, ExpiresAt: exp, User: u}, nil
}

func (s *AuthService) checkLock(email string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.attempts[email]; ok && now.Before(a.lockedUntil) {
		return domain.Throttled("Demasiados intentos. Intenta de nuevo en unos minutos.")
	}
	return nil
}

func (s *AuthService) recordFailure(email string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.attempts[email]
	if now.After(a.lockedUntil) && !a.lockedUntil.IsZero() {
		a = loginAttempt{} // a previous lockout already expired
	}
	a.fails++
	if a.fails >= maxLoginFailures {
		a.lockedUntil = now.Add(loginLockout)
	}
	s.attempts[email] = a
}

func (s *AuthService) clearFailures(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attempts, email)
}

// Authenticate resolves a bearer token into the current principal. The user is
// read fresh every time, so a role change or deactivation applies at once.
func (s *AuthService) Authenticate(ctx context.Context, token string) (domain.Principal, error) {
	if token == "" {
		return domain.Principal{}, domain.Unauthenticated("Inicia sesión para continuar.")
	}
	sess, found, err := s.sessions.Find(ctx, s.tokens.Digest(token))
	if err != nil {
		return domain.Principal{}, err
	}
	if !found || !s.clock.Now().Before(sess.ExpiresAt) {
		return domain.Principal{}, domain.Unauthenticated("Tu sesión venció. Inicia sesión de nuevo.")
	}
	u, err := s.users.Get(ctx, sess.UserID)
	if err != nil || !u.Active || !s.permits(u.Email, u.Role) {
		return domain.Principal{}, domain.Unauthenticated("Tu sesión venció. Inicia sesión de nuevo.")
	}
	return u.Principal(), nil
}

func (s *AuthService) Logout(ctx context.Context, token string) error {
	return s.sessions.Delete(ctx, s.tokens.Digest(token))
}

func (s *AuthService) Me(ctx context.Context, userID int64) (domain.User, error) {
	return s.users.Get(ctx, userID)
}

// ChangePassword lets a user replace their own password; every session of the
// user is closed, so they log in again with the new one.
func (s *AuthService) ChangePassword(ctx context.Context, userID int64, current, next string) error {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	if !s.hasher.Verify(u.PasswordHash, current) {
		return domain.Invalid("La contraseña actual no es correcta.")
	}
	return s.setPassword(ctx, &u, next)
}

// ResetPassword is the administrator's override for someone who forgot theirs.
func (s *AuthService) ResetPassword(ctx context.Context, userID int64, next string) error {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return err
	}
	return s.setPassword(ctx, &u, next)
}

func (s *AuthService) setPassword(ctx context.Context, u *domain.User, next string) error {
	if err := domain.ValidatePassword(next); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(next)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	if err := s.users.Update(ctx, u); err != nil {
		return err
	}
	return s.sessions.DeleteByUser(ctx, u.ID)
}

// ---------- user management ----------

type NewUser struct {
	Name       string
	Email      string
	Password   string
	Role       domain.Role
	TruckID    int64
	CustomerID int64
}

func (s *AuthService) CreateUser(ctx context.Context, in NewUser) (domain.User, error) {
	if !domain.ValidEmail(in.Email) {
		return domain.User{}, domain.Invalid("Ese correo no parece válido.")
	}
	if !s.permits(in.Email, in.Role) {
		return domain.User{}, domain.Rule("Solo los correos de la lista de acceso pueden ser administradores.")
	}
	if err := domain.ValidateUserFields(in.Name, in.Role, in.TruckID, in.CustomerID); err != nil {
		return domain.User{}, err
	}
	if err := domain.ValidatePassword(in.Password); err != nil {
		return domain.User{}, err
	}
	if in.TruckID != 0 {
		if _, err := s.trucks.Get(ctx, in.TruckID); err != nil {
			return domain.User{}, err
		}
	}
	if err := s.checkCustomer(ctx, in.CustomerID, 0); err != nil {
		return domain.User{}, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return domain.User{}, err
	}
	u := domain.User{
		Name: strings.TrimSpace(in.Name), Email: domain.NormalizeEmail(in.Email), PasswordHash: hash,
		Role: in.Role, Active: true, TruckID: in.TruckID, CustomerID: in.CustomerID,
	}
	if err := s.users.Create(ctx, &u); err != nil {
		return domain.User{}, err
	}
	return u, nil
}

// checkCustomer verifies the customer exists and has no other login (self is the
// user being edited, 0 when creating). customerID 0 means "not a commerce account".
func (s *AuthService) checkCustomer(ctx context.Context, customerID, self int64) error {
	if customerID == 0 {
		return nil
	}
	if _, err := s.customers.Get(ctx, customerID); err != nil {
		return err
	}
	all, err := s.users.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.CustomerID == customerID && o.ID != self {
			return domain.Conflict("Ese comercio ya tiene credenciales.")
		}
	}
	return nil
}

func (s *AuthService) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.users.List(ctx)
}

type EditUser struct {
	Name       string
	Role       domain.Role
	Active     bool
	TruckID    int64
	CustomerID int64
}

// UpdateUser edits a user. The business can never be left without an active owner.
func (s *AuthService) UpdateUser(ctx context.Context, id int64, in EditUser) (domain.User, error) {
	if err := domain.ValidateUserFields(in.Name, in.Role, in.TruckID, in.CustomerID); err != nil {
		return domain.User{}, err
	}
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	if !s.permits(u.Email, in.Role) {
		return domain.User{}, domain.Rule("Solo los correos de la lista de acceso pueden ser administradores.")
	}
	if u.Role == domain.RoleOwner && u.Active && (in.Role != domain.RoleOwner || !in.Active) {
		all, err := s.users.List(ctx)
		if err != nil {
			return domain.User{}, err
		}
		others := 0
		for _, o := range all {
			if o.ID != u.ID && o.Role == domain.RoleOwner && o.Active {
				others++
			}
		}
		if others == 0 {
			return domain.User{}, domain.Rule("Debe quedar al menos un administrador activo.")
		}
	}
	if in.TruckID != 0 {
		if _, err := s.trucks.Get(ctx, in.TruckID); err != nil {
			return domain.User{}, err
		}
	}
	if err := s.checkCustomer(ctx, in.CustomerID, id); err != nil {
		return domain.User{}, err
	}
	u.Name, u.Role, u.Active, u.TruckID, u.CustomerID = strings.TrimSpace(in.Name), in.Role, in.Active, in.TruckID, in.CustomerID
	if err := s.users.Update(ctx, &u); err != nil {
		return domain.User{}, err
	}
	if !u.Active {
		if err := s.sessions.DeleteByUser(ctx, u.ID); err != nil {
			return domain.User{}, err
		}
	}
	return u, nil
}

// ---------- access allowlist ----------

// RestrictTo limits who can be an owner (the only role that manages users) to
// the given emails: nobody else can be created, promoted, sign in or keep a
// session as owner (sessions are re-checked on every request, so removing an
// email takes effect at once). Accounts of other roles, created by the owners,
// are unaffected. An empty list lifts the restriction.
func (s *AuthService) RestrictTo(emails []string) {
	if len(emails) == 0 {
		s.allowed = nil
		return
	}
	s.allowed = make(map[string]struct{}, len(emails))
	for _, e := range emails {
		s.allowed[domain.NormalizeEmail(e)] = struct{}{}
	}
}

func (s *AuthService) permits(email string, role domain.Role) bool {
	if s.allowed == nil || role != domain.RoleOwner {
		return true
	}
	_, ok := s.allowed[domain.NormalizeEmail(email)]
	return ok
}

// SeedOwner is an owner account that must exist.
type SeedOwner struct {
	Name     string
	Email    string
	Password string
}

// EnsureOwners creates the owners that do not exist yet and leaves existing
// accounts untouched (never resets a password), so it is safe on every start.
// It returns the emails it created.
func (s *AuthService) EnsureOwners(ctx context.Context, owners []SeedOwner) ([]string, error) {
	var created []string
	for _, o := range owners {
		email := domain.NormalizeEmail(o.Email)
		_, found, err := s.users.FindByEmail(ctx, email)
		if err != nil {
			return created, err
		}
		if found {
			continue
		}
		if _, err := s.CreateUser(ctx, NewUser{Name: o.Name, Email: email, Password: o.Password, Role: domain.RoleOwner}); err != nil {
			return created, err
		}
		created = append(created, email)
	}
	return created, nil
}

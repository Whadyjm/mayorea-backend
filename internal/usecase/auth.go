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
	users    UserRepository
	sessions SessionRepository
	trucks   TruckRepository
	hasher   PasswordHasher
	tokens   Tokens
	clock    Clock
	ttl      time.Duration

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

func NewAuthService(u UserRepository, s SessionRepository, t TruckRepository, h PasswordHasher, tk Tokens, clock Clock, ttl time.Duration) (*AuthService, error) {
	dummy, err := h.Hash("mayorea-dummy-password")
	if err != nil {
		return nil, err
	}
	return &AuthService{users: u, sessions: s, trucks: t, hasher: h, tokens: tk, clock: clock, ttl: ttl,
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
	if err != nil || !u.Active {
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
	Name     string
	Email    string
	Password string
	Role     domain.Role
	TruckID  int64
}

func (s *AuthService) CreateUser(ctx context.Context, in NewUser) (domain.User, error) {
	if !domain.ValidEmail(in.Email) {
		return domain.User{}, domain.Invalid("Ese correo no parece válido.")
	}
	if err := domain.ValidateUserFields(in.Name, in.Role, in.TruckID); err != nil {
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
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return domain.User{}, err
	}
	u := domain.User{
		Name: strings.TrimSpace(in.Name), Email: domain.NormalizeEmail(in.Email), PasswordHash: hash,
		Role: in.Role, Active: true, TruckID: in.TruckID,
	}
	if err := s.users.Create(ctx, &u); err != nil {
		return domain.User{}, err
	}
	return u, nil
}

func (s *AuthService) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.users.List(ctx)
}

type EditUser struct {
	Name    string
	Role    domain.Role
	Active  bool
	TruckID int64
}

// UpdateUser edits a user. The business can never be left without an active owner.
func (s *AuthService) UpdateUser(ctx context.Context, id int64, in EditUser) (domain.User, error) {
	if err := domain.ValidateUserFields(in.Name, in.Role, in.TruckID); err != nil {
		return domain.User{}, err
	}
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return domain.User{}, err
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
			return domain.User{}, domain.Rule("Debe quedar al menos un dueño activo.")
		}
	}
	if in.TruckID != 0 {
		if _, err := s.trucks.Get(ctx, in.TruckID); err != nil {
			return domain.User{}, err
		}
	}
	u.Name, u.Role, u.Active, u.TruckID = strings.TrimSpace(in.Name), in.Role, in.Active, in.TruckID
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

// Bootstrap creates the first owner when the system has no users yet. It is a
// no-op afterwards, so it is safe to call on every start.
func (s *AuthService) Bootstrap(ctx context.Context, name, email, password string) (created bool, err error) {
	n, err := s.users.Count(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	_, err = s.CreateUser(ctx, NewUser{Name: name, Email: email, Password: password, Role: domain.RoleOwner})
	return err == nil, err
}

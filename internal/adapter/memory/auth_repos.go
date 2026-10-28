package memory

import (
	"context"
	"sort"
	"time"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

func (s *Store) Users() usecase.UserRepository       { return userRepo{s} }
func (s *Store) Sessions() usecase.SessionRepository { return sessionRepo{s} }

type userRepo struct{ s *Store }

func (r userRepo) Create(ctx context.Context, u *domain.User) error {
	defer r.s.lock(ctx)()
	for _, x := range r.s.users {
		if x.Email == u.Email {
			return domain.Conflict("Ya existe un usuario con ese correo.")
		}
	}
	u.ID = r.s.next("user")
	r.s.users[u.ID] = *u
	return nil
}

func (r userRepo) Get(ctx context.Context, id int64) (domain.User, error) {
	defer r.s.lock(ctx)()
	u, ok := r.s.users[id]
	if !ok {
		return domain.User{}, domain.NotFound("Usuario no encontrado.")
	}
	return u, nil
}

func (r userRepo) FindByEmail(ctx context.Context, email string) (domain.User, bool, error) {
	defer r.s.lock(ctx)()
	for _, u := range r.s.users {
		if u.Email == email {
			return u, true, nil
		}
	}
	return domain.User{}, false, nil
}

func (r userRepo) List(ctx context.Context) ([]domain.User, error) {
	defer r.s.lock(ctx)()
	out := make([]domain.User, 0, len(r.s.users))
	for _, u := range r.s.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r userRepo) Count(ctx context.Context) (int, error) {
	defer r.s.lock(ctx)()
	return len(r.s.users), nil
}

func (r userRepo) Update(ctx context.Context, u *domain.User) error {
	defer r.s.lock(ctx)()
	if _, ok := r.s.users[u.ID]; !ok {
		return domain.NotFound("Usuario no encontrado.")
	}
	r.s.users[u.ID] = *u
	return nil
}

type sessionRepo struct{ s *Store }

func (r sessionRepo) Create(ctx context.Context, sess domain.Session) error {
	defer r.s.lock(ctx)()
	r.s.sessions[sess.Digest] = sess
	return nil
}

func (r sessionRepo) Find(ctx context.Context, digest string) (domain.Session, bool, error) {
	defer r.s.lock(ctx)()
	sess, ok := r.s.sessions[digest]
	return sess, ok, nil
}

func (r sessionRepo) Delete(ctx context.Context, digest string) error {
	defer r.s.lock(ctx)()
	delete(r.s.sessions, digest)
	return nil
}

func (r sessionRepo) DeleteByUser(ctx context.Context, userID int64) error {
	defer r.s.lock(ctx)()
	for d, sess := range r.s.sessions {
		if sess.UserID == userID {
			delete(r.s.sessions, d)
		}
	}
	return nil
}

func (r sessionRepo) DeleteExpired(ctx context.Context, now time.Time) error {
	defer r.s.lock(ctx)()
	for d, sess := range r.s.sessions {
		if !now.Before(sess.ExpiresAt) {
			delete(r.s.sessions, d)
		}
	}
	return nil
}

package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

func (d *DB) Users() usecase.UserRepository       { return userRepo{d} }
func (d *DB) Sessions() usecase.SessionRepository { return sessionRepo{d} }

type userRepo struct{ d *DB }

const userSelect = `select id, name, email, password_hash, role, active, coalesce(truck_id, 0) from users`

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.Active, &u.TruckID)
	return u, err
}

// truckArg maps "no truck" (0) to SQL NULL.
func truckArg(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func (r userRepo) Create(ctx context.Context, u *domain.User) error {
	err := r.d.q(ctx).QueryRow(ctx,
		`insert into users (name, email, password_hash, role, active, truck_id) values ($1, $2, $3, $4, $5, $6) returning id`,
		u.Name, u.Email, u.PasswordHash, u.Role, u.Active, truckArg(u.TruckID)).Scan(&u.ID)
	if isUniqueViolation(err) {
		return domain.Conflict("Ya existe un usuario con ese correo.")
	}
	return err
}

func (r userRepo) Get(ctx context.Context, id int64) (domain.User, error) {
	u, err := scanUser(r.d.q(ctx).QueryRow(ctx, userSelect+` where id = $1`, id))
	return u, notFoundIf(err, "Usuario")
}

func (r userRepo) FindByEmail(ctx context.Context, email string) (domain.User, bool, error) {
	u, err := scanUser(r.d.q(ctx).QueryRow(ctx, userSelect+` where email = $1`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, false, nil
	}
	return u, err == nil, err
}

func (r userRepo) List(ctx context.Context) ([]domain.User, error) {
	return queryAll(ctx, r.d.q(ctx), func(rows pgx.Rows) (domain.User, error) { return scanUser(rows) }, userSelect+` order by id`)
}

func (r userRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.d.q(ctx).QueryRow(ctx, `select count(*) from users`).Scan(&n)
	return n, err
}

func (r userRepo) Update(ctx context.Context, u *domain.User) error {
	tag, err := r.d.q(ctx).Exec(ctx,
		`update users set name = $2, role = $3, active = $4, truck_id = $5, password_hash = $6 where id = $1`,
		u.ID, u.Name, u.Role, u.Active, truckArg(u.TruckID), u.PasswordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("Usuario no encontrado.")
	}
	return nil
}

type sessionRepo struct{ d *DB }

func (r sessionRepo) Create(ctx context.Context, s domain.Session) error {
	_, err := r.d.q(ctx).Exec(ctx, `insert into sessions (token_hash, user_id, expires_at) values ($1, $2, $3)`,
		s.Digest, s.UserID, s.ExpiresAt)
	return err
}

func (r sessionRepo) Find(ctx context.Context, digest string) (domain.Session, bool, error) {
	s := domain.Session{Digest: digest}
	err := r.d.q(ctx).QueryRow(ctx, `select user_id, expires_at from sessions where token_hash = $1`, digest).
		Scan(&s.UserID, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, false, nil
	}
	return s, err == nil, err
}

func (r sessionRepo) Delete(ctx context.Context, digest string) error {
	_, err := r.d.q(ctx).Exec(ctx, `delete from sessions where token_hash = $1`, digest)
	return err
}

func (r sessionRepo) DeleteByUser(ctx context.Context, userID int64) error {
	_, err := r.d.q(ctx).Exec(ctx, `delete from sessions where user_id = $1`, userID)
	return err
}

func (r sessionRepo) DeleteExpired(ctx context.Context, now time.Time) error {
	_, err := r.d.q(ctx).Exec(ctx, `delete from sessions where expires_at <= $1`, now)
	return err
}

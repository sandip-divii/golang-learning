// Package repository is the only package in the project that speaks SQL.
//
// It exposes an interface (the contract) and one MySQL implementation of it.
// Swapping MySQL for PostgreSQL later means writing a second implementation --
// no layer above this one changes.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"

	"github.com/sandip/docker-gin-go-user-api/internal/model"
)

// Sentinel errors. The service layer translates these into its own domain
// errors so that nothing above the repository has to know MySQL error codes.
var (
	ErrNotFound       = errors.New("repository: user not found")
	ErrDuplicateEmail = errors.New("repository: email already exists")
)

// UserRepository is the contract the service depends on.
//
// This is the same idea as the Animal interface from the earlier notes, doing
// real work: the service accepts anything that can do these five things, so the
// tests can hand it a map pretending to be a database.
type UserRepository interface {
	Create(ctx context.Context, u *model.User) (int, error)
	GetByID(ctx context.Context, id int) (*model.User, error)
	GetAll(ctx context.Context) ([]model.User, error)
	Update(ctx context.Context, u *model.User) error
	Delete(ctx context.Context, id int) error
}

// MySQLUserRepo is the real implementation, backed by database/sql.
type MySQLUserRepo struct {
	db *sql.DB
}

// NewMySQLUserRepo wires the repository to an open connection pool.
func NewMySQLUserRepo(db *sql.DB) *MySQLUserRepo {
	return &MySQLUserRepo{db: db}
}

// compile-time check: if MySQLUserRepo ever stops satisfying the interface,
// the build fails here rather than somewhere confusing.
var _ UserRepository = (*MySQLUserRepo)(nil)

// Create inserts a row and returns the new auto-increment id.
func (r *MySQLUserRepo) Create(ctx context.Context, u *model.User) (int, error) {
	const q = `INSERT INTO users (name, email) VALUES (?, ?)`

	res, err := r.db.ExecContext(ctx, q, u.Name, u.Email)
	if err != nil {
		if isDuplicateKey(err) {
			return 0, ErrDuplicateEmail
		}
		return 0, fmt.Errorf("create user: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create user: read insert id: %w", err)
	}

	return int(id), nil
}

// GetByID returns one user, or ErrNotFound.
func (r *MySQLUserRepo) GetByID(ctx context.Context, id int) (*model.User, error) {
	const q = `SELECT id, name, email, created_at FROM users WHERE id = ?`

	var u model.User
	err := r.db.QueryRowContext(ctx, q, id).
		Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user %d: %w", id, err)
	}

	return &u, nil
}

// GetAll returns every user, newest last.
func (r *MySQLUserRepo) GetAll(ctx context.Context) ([]model.User, error) {
	const q = `SELECT id, name, email, created_at FROM users ORDER BY id`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	// Start non-nil so an empty table marshals to [] and not to null.
	users := make([]model.User, 0)

	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("list users: scan: %w", err)
		}
		users = append(users, u)
	}

	// rows.Err() reports failures that happened mid-iteration. Skipping this
	// check is a classic way to silently return a truncated list.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}

	return users, nil
}

// Update overwrites name and email for an existing id.
func (r *MySQLUserRepo) Update(ctx context.Context, u *model.User) error {
	const q = `UPDATE users SET name = ?, email = ? WHERE id = ?`

	res, err := r.db.ExecContext(ctx, q, u.Name, u.Email, u.ID)
	if err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicateEmail
		}
		return fmt.Errorf("update user %d: %w", u.ID, err)
	}

	return checkAffected(res, "update", u.ID)
}

// Delete removes a row by id.
func (r *MySQLUserRepo) Delete(ctx context.Context, id int) error {
	const q = `DELETE FROM users WHERE id = ?`

	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete user %d: %w", id, err)
	}

	return checkAffected(res, "delete", id)
}

// checkAffected turns "0 rows changed" into ErrNotFound.
func checkAffected(res sql.Result, op string, id int) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s user %d: %w", op, id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// isDuplicateKey reports whether err is MySQL error 1062 (duplicate entry for
// a unique key) -- here, the UNIQUE constraint on users.email.
func isDuplicateKey(err error) bool {
	var myErr *mysql.MySQLError
	return errors.As(err, &myErr) && myErr.Number == 1062
}

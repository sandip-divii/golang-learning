// Package service holds the business rules.
//
// It knows nothing about HTTP, Gin or SQL -- which is exactly why it can be
// tested in milliseconds with a fake repository and no database.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sandip/docker-gin-go-user-api/internal/model"
	"github.com/sandip/docker-gin-go-user-api/internal/repository"
)

// Domain errors. The handler maps these to HTTP status codes; nothing else in
// the project needs to know what a 404 is.
var (
	ErrUserNotFound = errors.New("user not found")
	ErrEmailExists  = errors.New("email already exists")
)

// ValidationError is returned when the caller sent something we refuse to
// store. It carries a message safe to show the client.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// UserService is the rule-keeper sitting between handler and repository.
type UserService struct {
	repo repository.UserRepository
}

// NewUserService injects the repository dependency.
func NewUserService(repo repository.UserRepository) *UserService {
	return &UserService{repo: repo}
}

// CreateUser validates the input, then stores it.
func (s *UserService) CreateUser(ctx context.Context, name, email string) (*model.User, error) {
	name, email, err := normalize(name, email)
	if err != nil {
		return nil, err
	}

	id, err := s.repo.Create(ctx, &model.User{Name: name, Email: email})
	if err != nil {
		return nil, translate(err)
	}

	// Read the row back so the response carries the real created_at that the
	// database generated, not a value we guessed.
	return s.GetUser(ctx, id)
}

// GetUser fetches one user by id.
func (s *UserService) GetUser(ctx context.Context, id int) (*model.User, error) {
	if id <= 0 {
		return nil, &ValidationError{Field: "id", Message: "id must be a positive number"}
	}

	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, translate(err)
	}

	return u, nil
}

// ListUsers returns every user.
func (s *UserService) ListUsers(ctx context.Context) ([]model.User, error) {
	users, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, translate(err)
	}
	return users, nil
}

// UpdateUser validates, then overwrites name and email.
func (s *UserService) UpdateUser(ctx context.Context, id int, name, email string) (*model.User, error) {
	if id <= 0 {
		return nil, &ValidationError{Field: "id", Message: "id must be a positive number"}
	}

	name, email, err := normalize(name, email)
	if err != nil {
		return nil, err
	}

	if err := s.repo.Update(ctx, &model.User{ID: id, Name: name, Email: email}); err != nil {
		return nil, translate(err)
	}

	return s.GetUser(ctx, id)
}

// DeleteUser removes a user by id.
func (s *UserService) DeleteUser(ctx context.Context, id int) error {
	if id <= 0 {
		return &ValidationError{Field: "id", Message: "id must be a positive number"}
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return translate(err)
	}

	return nil
}

// normalize trims the name, lowercases the email, and rejects both if invalid.
//
// Lowercasing matters: without it "Alice@x.com" and "alice@x.com" would be two
// different people, and the UNIQUE index would not catch it.
func normalize(name, email string) (string, string, error) {
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))

	if name == "" {
		return "", "", &ValidationError{Field: "name", Message: "name is required"}
	}
	if len(name) > 100 {
		return "", "", &ValidationError{Field: "name", Message: "name must be 100 characters or fewer"}
	}
	if !validEmail(email) {
		return "", "", &ValidationError{Field: "email", Message: "a valid email is required"}
	}

	return name, email, nil
}

// validEmail is a deliberately simple check: exactly one @, with something on
// each side of it. Real address validation is a rabbit hole; the database's
// UNIQUE constraint and a confirmation email are the real defences.
func validEmail(email string) bool {
	if email == "" || len(email) > 255 {
		return false
	}

	at := strings.Index(email, "@")
	if at <= 0 || at != strings.LastIndex(email, "@") || at == len(email)-1 {
		return false
	}

	// The domain half needs a dot that is not at either edge.
	domain := email[at+1:]
	dot := strings.Index(domain, ".")

	return dot > 0 && dot < len(domain)-1
}

// translate converts repository errors into domain errors, so the layers above
// never import the repository package just to check an error.
func translate(err error) error {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return ErrUserNotFound
	case errors.Is(err, repository.ErrDuplicateEmail):
		return ErrEmailExists
	default:
		return fmt.Errorf("service: %w", err)
	}
}

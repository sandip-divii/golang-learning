package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sandip/docker-gin-go-user-api/internal/model"
	"github.com/sandip/docker-gin-go-user-api/internal/repository"
)

// ---------------------------------------------------------------------------
// The fake repository
//
// A Go map pretending to be a database. It satisfies repository.UserRepository,
// so the service cannot tell the difference -- this is the Animal/Dog/Cat
// interface idea doing real work. No MySQL needed to run these tests.
// ---------------------------------------------------------------------------

type fakeRepo struct {
	users  map[int]model.User
	nextID int
	failOn string // set to a method name to simulate an infrastructure failure
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: make(map[int]model.User), nextID: 1}
}

var _ repository.UserRepository = (*fakeRepo)(nil)

func (f *fakeRepo) Create(_ context.Context, u *model.User) (int, error) {
	if f.failOn == "Create" {
		return 0, errors.New("boom")
	}
	for _, existing := range f.users {
		if existing.Email == u.Email {
			return 0, repository.ErrDuplicateEmail
		}
	}

	id := f.nextID
	f.nextID++
	f.users[id] = model.User{ID: id, Name: u.Name, Email: u.Email, CreatedAt: time.Now()}

	return id, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id int) (*model.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return &u, nil
}

func (f *fakeRepo) GetAll(_ context.Context) ([]model.User, error) {
	out := make([]model.User, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeRepo) Update(_ context.Context, u *model.User) error {
	if _, ok := f.users[u.ID]; !ok {
		return repository.ErrNotFound
	}
	for id, existing := range f.users {
		if id != u.ID && existing.Email == u.Email {
			return repository.ErrDuplicateEmail
		}
	}

	current := f.users[u.ID]
	current.Name = u.Name
	current.Email = u.Email
	f.users[u.ID] = current

	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id int) error {
	if _, ok := f.users[id]; !ok {
		return repository.ErrNotFound
	}
	delete(f.users, id)
	return nil
}

// ---------------------------------------------------------------------------
// Table-driven tests
// ---------------------------------------------------------------------------

func TestCreateUser(t *testing.T) {
	tests := []struct {
		name      string
		userName  string
		email     string
		wantValid bool   // true when we expect a *ValidationError
		wantEmail string // expected stored email, when creation succeeds
	}{
		{name: "valid user", userName: "Alice", email: "alice@example.com", wantEmail: "alice@example.com"},
		{name: "email is lowercased", userName: "Bob", email: "BOB@Example.COM", wantEmail: "bob@example.com"},
		{name: "name is trimmed", userName: "  Carol  ", email: "carol@example.com", wantEmail: "carol@example.com"},
		{name: "empty name", userName: "", email: "x@example.com", wantValid: true},
		{name: "whitespace-only name", userName: "   ", email: "x@example.com", wantValid: true},
		{name: "email without @", userName: "Dave", email: "dave.example.com", wantValid: true},
		{name: "email without domain dot", userName: "Erin", email: "erin@localhost", wantValid: true},
		{name: "email missing local part", userName: "Frank", email: "@example.com", wantValid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewUserService(newFakeRepo())

			got, err := svc.CreateUser(context.Background(), tt.userName, tt.email)

			if tt.wantValid {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected a ValidationError, got %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Email != tt.wantEmail {
				t.Errorf("email: expected %q, got %q", tt.wantEmail, got.Email)
			}
			if got.ID == 0 {
				t.Error("expected a non-zero id")
			}
		})
	}
}

func TestCreateUserDuplicateEmail(t *testing.T) {
	svc := NewUserService(newFakeRepo())
	ctx := context.Background()

	if _, err := svc.CreateUser(ctx, "Alice", "alice@example.com"); err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	// Different capitalisation must still collide, because we lowercase first.
	_, err := svc.CreateUser(ctx, "Alice Again", "ALICE@example.com")
	if !errors.Is(err, ErrEmailExists) {
		t.Fatalf("expected ErrEmailExists, got %v", err)
	}
}

func TestGetUser(t *testing.T) {
	svc := NewUserService(newFakeRepo())
	ctx := context.Background()

	created, err := svc.CreateUser(ctx, "Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	tests := []struct {
		name    string
		id      int
		wantErr error
	}{
		{name: "existing user", id: created.ID},
		{name: "missing user", id: 999, wantErr: ErrUserNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.GetUser(ctx, tt.id)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ID != tt.id {
				t.Errorf("expected id %d, got %d", tt.id, got.ID)
			}
		})
	}
}

func TestGetUserRejectsBadID(t *testing.T) {
	svc := NewUserService(newFakeRepo())

	for _, id := range []int{0, -1} {
		_, err := svc.GetUser(context.Background(), id)

		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("id %d: expected a ValidationError, got %v", id, err)
		}
	}
}

func TestUpdateUser(t *testing.T) {
	svc := NewUserService(newFakeRepo())
	ctx := context.Background()

	created, err := svc.CreateUser(ctx, "Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	updated, err := svc.UpdateUser(ctx, created.ID, "Alice Smith", "alice.smith@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Name != "Alice Smith" {
		t.Errorf("expected name %q, got %q", "Alice Smith", updated.Name)
	}
	if updated.Email != "alice.smith@example.com" {
		t.Errorf("expected email %q, got %q", "alice.smith@example.com", updated.Email)
	}

	if _, err := svc.UpdateUser(ctx, 999, "Ghost", "ghost@example.com"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}
}

func TestDeleteUser(t *testing.T) {
	svc := NewUserService(newFakeRepo())
	ctx := context.Background()

	created, err := svc.CreateUser(ctx, "Alice", "alice@example.com")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := svc.DeleteUser(ctx, created.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Deleting twice must report not-found the second time.
	if err := svc.DeleteUser(ctx, created.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got %v", err)
	}

	if _, err := svc.GetUser(ctx, created.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected the user to be gone, got %v", err)
	}
}

func TestListUsers(t *testing.T) {
	svc := NewUserService(newFakeRepo())
	ctx := context.Background()

	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected an empty list, got %d", len(users))
	}

	if _, err := svc.CreateUser(ctx, "Alice", "alice@example.com"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if _, err := svc.CreateUser(ctx, "Bob", "bob@example.com"); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	users, err = svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

func TestRepositoryFailureIsWrapped(t *testing.T) {
	repo := newFakeRepo()
	repo.failOn = "Create"
	svc := NewUserService(repo)

	_, err := svc.CreateUser(context.Background(), "Alice", "alice@example.com")
	if err == nil {
		t.Fatal("expected an error")
	}

	// An infrastructure failure must NOT be mistaken for a domain error --
	// otherwise the handler would return 404 or 409 for a broken database.
	if errors.Is(err, ErrUserNotFound) || errors.Is(err, ErrEmailExists) {
		t.Errorf("infrastructure failure leaked as a domain error: %v", err)
	}
}

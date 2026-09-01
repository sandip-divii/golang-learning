package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sandip/docker-gin-go-user-api/internal/model"
	"github.com/sandip/docker-gin-go-user-api/internal/repository"
	"github.com/sandip/docker-gin-go-user-api/internal/service"
	"github.com/sandip/docker-gin-go-user-api/internal/worker"
)

// ---------------------------------------------------------------------------
// A fake repository again -- so these tests exercise routing, JSON encoding and
// status-code mapping without a database anywhere in sight.
// ---------------------------------------------------------------------------

type fakeRepo struct {
	users  map[int]model.User
	nextID int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: make(map[int]model.User), nextID: 1}
}

var _ repository.UserRepository = (*fakeRepo)(nil)

func (f *fakeRepo) Create(_ context.Context, u *model.User) (int, error) {
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
	current := f.users[u.ID]
	current.Name, current.Email = u.Name, u.Email
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

// newTestRouter builds a real Gin engine wired to a fake repository.
func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)

	router := gin.New()

	// A real worker with a small buffer: created but its goroutine started too,
	// so enqueued jobs are consumed instead of piling up during the test run.
	jobs := worker.New(16)
	jobs.Start()

	NewUserHandler(service.NewUserService(newFakeRepo()), jobs).RegisterRoutes(router)

	return router
}

// call sends a request through the router using httptest -- no network, no port.
func call(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec
}

// TestEndpoints walks the full CRUD lifecycle in order, checking status codes.
func TestEndpoints(t *testing.T) {
	router := newTestRouter()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{"health check", http.MethodGet, "/health", "", http.StatusOK},
		{"list is empty", http.MethodGet, "/users", "", http.StatusOK},
		{"create alice", http.MethodPost, "/users", `{"name":"Alice","email":"alice@example.com"}`, http.StatusCreated},
		{"create bob", http.MethodPost, "/users", `{"name":"Bob","email":"bob@example.com"}`, http.StatusCreated},
		{"duplicate email", http.MethodPost, "/users", `{"name":"Alice2","email":"ALICE@example.com"}`, http.StatusConflict},
		{"invalid email", http.MethodPost, "/users", `{"name":"X","email":"not-an-email"}`, http.StatusBadRequest},
		{"missing name", http.MethodPost, "/users", `{"email":"y@example.com"}`, http.StatusBadRequest},
		{"malformed json", http.MethodPost, "/users", `{`, http.StatusBadRequest},
		{"list has two", http.MethodGet, "/users", "", http.StatusOK},
		{"get existing", http.MethodGet, "/users/1", "", http.StatusOK},
		{"get missing", http.MethodGet, "/users/999", "", http.StatusNotFound},
		{"non-numeric id", http.MethodGet, "/users/abc", "", http.StatusBadRequest},
		{"update existing", http.MethodPut, "/users/1", `{"name":"Alice Smith","email":"alice.smith@example.com"}`, http.StatusOK},
		{"update missing", http.MethodPut, "/users/999", `{"name":"G","email":"g@example.com"}`, http.StatusNotFound},
		{"delete existing", http.MethodDelete, "/users/2", "", http.StatusOK},
		{"get deleted", http.MethodGet, "/users/2", "", http.StatusNotFound},
		{"delete again", http.MethodDelete, "/users/2", "", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := call(router, tt.method, tt.path, tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("%s %s: expected %d, got %d (body: %s)",
					tt.method, tt.path, tt.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestEmptyListIsArrayNotNull guards a small but annoying JSON bug: a nil slice
// marshals to `null`, which breaks front-ends expecting an array.
func TestEmptyListIsArrayNotNull(t *testing.T) {
	rec := call(newTestRouter(), http.MethodGet, "/users", "")

	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("expected [], got %s", got)
	}

	var users []model.User
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

// TestCreateReturnsCreatedUser checks the response body, not just the status.
func TestCreateReturnsCreatedUser(t *testing.T) {
	rec := call(newTestRouter(), http.MethodPost, "/users",
		`{"name":"Alice","email":"Alice@Example.COM"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var user model.User
	if err := json.Unmarshal(rec.Body.Bytes(), &user); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if user.ID == 0 {
		t.Error("expected a non-zero id")
	}
	if user.Email != "alice@example.com" {
		t.Errorf("expected the email to be lowercased, got %q", user.Email)
	}
}

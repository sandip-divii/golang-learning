// Package handler is the only package in the project that speaks HTTP.
//
// Its whole job is translation: URL and JSON in, JSON and a status code out.
// No business rules live here.
package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/sandip/docker-gin-go-user-api/internal/service"
)

// UserHandler holds the one dependency it needs: the service.
type UserHandler struct {
	svc *service.UserService
}

// NewUserHandler injects the service dependency.
func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// Request bodies. The `binding:"..."` tags are Gin's validator running at the
// edge -- it rejects a malformed body before our code ever sees it.
//
// The service validates again. That is deliberate, not duplication by accident:
// the service must stay correct even when called from a CLI or a test, where
// Gin is not in the picture.
type createUserRequest struct {
	Name  string `json:"name"  binding:"required"`
	Email string `json:"email" binding:"required,email"`
}

type updateUserRequest struct {
	Name  string `json:"name"  binding:"required"`
	Email string `json:"email" binding:"required,email"`
}

// RegisterRoutes attaches every endpoint to the Gin engine.
//
// This is the Gin equivalent of the http.HandleFunc calls in the net/http
// version -- but with typed path parameters and route groups.
func (h *UserHandler) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", h.Health)

	users := r.Group("/users")
	{
		users.POST("", h.Create)
		users.GET("", h.List)
		users.GET("/:id", h.GetByID)
		users.PUT("/:id", h.Update)
		users.DELETE("/:id", h.Delete)
	}
}

// Health is a liveness probe: no database, no logic, just proof the process is up.
func (h *UserHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Create handles POST /users.
func (h *UserHandler) Create(c *gin.Context) {
	var req createUserRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	// c.Request.Context() carries the client's cancellation all the way down to
	// the SQL driver: hang up the connection and the query is abandoned too.
	user, err := h.svc.CreateUser(c.Request.Context(), req.Name, req.Email)
	if err != nil {
		h.fail(c, err)
		return
	}

	c.JSON(http.StatusCreated, user)
}

// List handles GET /users.
func (h *UserHandler) List(c *gin.Context) {
	users, err := h.svc.ListUsers(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}

	c.JSON(http.StatusOK, users)
}

// GetByID handles GET /users/:id.
func (h *UserHandler) GetByID(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	user, err := h.svc.GetUser(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}

	c.JSON(http.StatusOK, user)
}

// Update handles PUT /users/:id.
func (h *UserHandler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}

	user, err := h.svc.UpdateUser(c.Request.Context(), id, req.Name, req.Email)
	if err != nil {
		h.fail(c, err)
		return
	}

	c.JSON(http.StatusOK, user)
}

// Delete handles DELETE /users/:id.
func (h *UserHandler) Delete(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	if err := h.svc.DeleteUser(c.Request.Context(), id); err != nil {
		h.fail(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

// parseID pulls :id out of the URL and converts it to an int.
// It writes the 400 itself and returns false when the value is not a number.
func parseID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must be a positive number"})
		return 0, false
	}
	return id, true
}

// fail is the single place where a domain error becomes a status code.
//
// Keeping this in one function is what stops status-code decisions from being
// scattered across six handlers and slowly drifting apart.
func (h *UserHandler) fail(c *gin.Context, err error) {
	var ve *service.ValidationError

	switch {
	case errors.Is(err, service.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})

	case errors.Is(err, service.ErrEmailExists):
		c.JSON(http.StatusConflict, gin.H{"error": "email already exists"})

	case errors.As(err, &ve):
		c.JSON(http.StatusBadRequest, gin.H{"error": ve.Message})

	default:
		// Log the detail for us; send the client something generic.
		// Leaking SQL errors to callers is an information disclosure bug.
		log.Printf("unexpected error on %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

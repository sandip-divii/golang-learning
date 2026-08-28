# gin-go-user-api

The same user API as `go-user-api`, rebuilt on the **Gin** framework instead of
`net/http`. Same four layers, same six endpoints, same database — so the only
thing that changed is the HTTP plumbing. That is the point: it makes the
framework's contribution visible.

- **Location:** `C:\Users\sandi\IdeaProjects\go-learning\gin-go-user-api`
- **Database:** MySQL (XAMPP MariaDB on port **3307**, database `go_user_db`)
- **Port:** **8093** (the net/http version uses 8092, so both can run at once)

---

## Setup

### 1. Database

If you still have `go_user_db` from the net/http project, skip this — the schema
is identical. Otherwise run `schema.sql` in phpMyAdmin.

### 2. Dependencies

```bat
cd C:\Users\sandi\IdeaProjects\go-learning\gin-go-user-api
go mod tidy
```

This downloads Gin and the MySQL driver and writes `go.sum`.

### 3. Run

```bat
go run ./cmd/server
```

Expected output:

```
connected to MySQL
listening on http://localhost:8093
[GIN-debug] GET    /health   --> ...
```

Press `Ctrl+C` to stop — the server drains in-flight requests before exiting.

### 4. Tests

```bat
go test ./...
```

No database required: both test files use an in-memory fake repository.

---

## Endpoints

| Method | Path | Purpose | Success | Errors |
|---|---|---|---|---|
| GET | `/health` | Liveness probe | 200 | — |
| POST | `/users` | Create a user | 201 | 400, 409 |
| GET | `/users` | List all users | 200 | — |
| GET | `/users/:id` | Fetch one user | 200 | 400, 404 |
| PUT | `/users/:id` | Update a user | 200 | 400, 404, 409 |
| DELETE | `/users/:id` | Delete a user | 200 | 400, 404 |

### Example

```bat
curl http://localhost:8093/health

curl -X POST http://localhost:8093/users ^
  -H "Content-Type: application/json" ^
  -d "{\"name\":\"Sandip\",\"email\":\"sandip@example.com\"}"

curl http://localhost:8093/users
curl http://localhost:8093/users/1

curl -X PUT http://localhost:8093/users/1 ^
  -H "Content-Type: application/json" ^
  -d "{\"name\":\"Sandip M\",\"email\":\"sandip.m@example.com\"}"

curl -X DELETE http://localhost:8093/users/1
```

In Windows CMD the line-continuation character is `^`. In PowerShell it is a
backtick, and `curl` is an alias for `Invoke-WebRequest` — use `curl.exe`
explicitly to get the real tool.

---

## Configuration

Both are environment variables, so nothing needs recompiling:

| Variable | Default | Notes |
|---|---|---|
| `DB_DSN` | `root:@tcp(127.0.0.1:3307)/go_user_db?parseTime=true` | `parseTime=true` is required, or `created_at` will not scan into `time.Time` |
| `PORT` | `8093` | |

```bat
set PORT=9000
set DB_DSN=root:secret@tcp(127.0.0.1:3306)/go_user_db?parseTime=true
go run ./cmd/server
```

---

## Layout

```
gin-go-user-api/
├── go.mod
├── schema.sql
├── cmd/server/main.go                       ← wiring + startup + shutdown
└── internal/
    ├── model/user.go                        ← the shared User shape
    ├── repository/user_repository.go        ← the only file that speaks SQL
    ├── service/
    │   ├── user_service.go                  ← business rules
    │   └── user_service_test.go             ← 8 tests, fake repository
    └── handler/
        ├── user_handler.go                  ← the only file that speaks HTTP
        └── user_handler_test.go             ← 17 endpoint cases via httptest
```

Requests flow one way and one way only:

```
Client → Gin → Handler → Service → Repository → MySQL
```

---

## What Gin changed

| | net/http version | Gin version |
|---|---|---|
| Path parameters | Parse the URL string by hand | `c.Param("id")` |
| Routing | One `HandleFunc` per path, method checked inside | `r.GET`, `r.POST`, … + route groups |
| JSON decode | `json.NewDecoder(r.Body).Decode(&req)` | `c.ShouldBindJSON(&req)` |
| JSON encode | Set header, marshal, write | `c.JSON(status, value)` |
| Input validation | Hand-written `if` checks | `binding:"required,email"` struct tags |
| Panic safety | Crashes the server | `Recovery()` middleware → 500 |
| Request logging | Write it yourself | `Logger()` middleware |

What Gin did **not** change: the service, repository and model packages are
byte-for-byte the same idea as before. They never imported `net/http`, so they
never had to care that the framework changed. That is the payoff of the layering.

# docker-gin-go-user-api

A user CRUD API written in Go, built with Claude as a learning exercise, and this
week moved into Docker. This README answers the three assignment questions.

---

## 1. What does it do?

It's a REST API for managing users (create, read, update, delete), built on the
**Gin** framework and backed by **MySQL**, and it runs entirely in Docker:

```
docker compose up --build
```

That one command builds the Go binary in a multi-stage Dockerfile, starts a
MySQL 8 container, loads `schema.sql` into it automatically on first run, waits
for the database's healthcheck to pass, and then starts the API on
**http://localhost:8093**.

| Method | Path | Purpose | Success | Errors |
|---|---|---|---|---|
| GET | `/health` | Liveness probe | 200 | — |
| POST | `/users` | Create a user | 201 | 400, 409 |
| GET | `/users` | List all users | 200 | — |
| GET | `/users/:id` | Fetch one user | 200 | 400, 404 |
| PUT | `/users/:id` | Update a user | 200 | 400, 404, 409 |
| DELETE | `/users/:id` | Delete a user | 200 | 400, 404 |

Internally it is four layers, and requests flow one way only:

```
Client → Gin → Handler → Service → Repository → MySQL
```

- `internal/handler` — the only package that speaks HTTP
- `internal/service` — business rules (validation, duplicate-email checks)
- `internal/repository` — the only package that speaks SQL
- `internal/model` — the shared `User` struct

Configuration comes from environment variables (`DB_DSN`, `PORT`, `GIN_MODE`),
with a `.env` file for local development (`.env.example` is the template — the
real `.env` is never committed). Tests (`go test ./...`) need no database at
all: they run against an in-memory fake repository.

---

## 2. Where did Claude get it right, and where did I have to fix its output?

**What Claude got right.** The overall structure held up well: the four-layer
layout with the repository interface meant the service and handler code didn't
change at all when we moved from a host database to a containerized one. The
multi-stage Dockerfile was right on the first pass (build stage on
`golang:1.22-alpine`, tiny `alpine` runtime stage, static binary with
`CGO_ENABLED=0`, non-root user), and so were the production touches I wouldn't
have thought to ask for — graceful shutdown on Ctrl+C, HTTP server timeouts,
connection-pool limits, and failing fast with a `Ping` at startup instead of on
the first request.

**What I had to fix.** The fixes were mostly about *my machine and my setup*,
which Claude couldn't know:

- **Port collisions.** The compose file first mapped MySQL to a host port that
  was already taken on my machine (3306 is in use, and 3307 belongs to my XAMPP
  MariaDB from the earlier non-Docker version). I changed the mapping to
  `3308:3306`.
- **The DSN inside Docker.** The default connection string pointed at
  `127.0.0.1`, which works when the API runs on the host but not from inside a
  container — there, "localhost" is the container itself. The compose
  environment had to use the service name: `tcp(db:3306)`.
- **Startup ordering.** On the first tries the API container raced MySQL and
  died before the database was ready. The fix was a proper healthcheck on the
  `db` service plus `depends_on: condition: service_healthy`, not just plain
  `depends_on`.
- **A stale README.** The previous README still described the non-Docker XAMPP
  setup and even a hardcoded path on my machine — it had been carried over from
  the earlier project and needed rewriting (this file).

The lesson: Claude is reliable on structure and Go idioms, but everything that
touches my specific environment — ports, hostnames, what's already running —
still needs a human to test and correct.

---

## 3. Which Go concept confused me most, and what do I understand now that I didn't on Monday?

**Pointers, and mutexes.**

**Pointers.** On Monday, `*` and `&` felt like noise I copied without
understanding, and I couldn't have said why one function takes `*model.User`
while another returns `[]model.User`. Now I can read them: `&` takes the
address of a value, `*` follows an address back to the value, and the choice is
about *sharing vs copying*. `Create(ctx, u *model.User)` takes a pointer so the
repository can write the generated ID back into the caller's struct — with a
copy, the caller would never see it. `GetByID` returns `*model.User` so it can
return `nil` when there is no user. And methods use pointer receivers like
`func (r *MySQLUserRepo) Create(...)` so every call sees the same repo (and the
same `*sql.DB` pool) instead of a copy of it. The constructor line
`return &MySQLUserRepo{db: db}` stopped looking mysterious once I understood
it's just "build the struct, hand back its address."

**Mutexes.** My Monday confusion was more basic than the syntax: I didn't see
*why* a lock was ever needed — the code runs line by line, so what's there to
protect? The missing piece was that an HTTP server is concurrent by default:
Gin handles every request in its own goroutine, so two requests can touch the
same data at the same instant, and a plain map or counter gets corrupted
(a data race). A `sync.Mutex` makes goroutines take turns: `Lock()`, touch the
shared thing, `Unlock()` — usually as `defer mu.Unlock()` right after locking,
so the lock is released even on an early return or panic. I also now understand
why *this* project doesn't need one anywhere: the shared state lives in MySQL,
and `*sql.DB` is already safe for concurrent use — the mutex work is being done
for me inside the pool and the database. If I ever swap the repository for an
in-memory `map[int]model.User` implementation, that's exactly where a
`sync.RWMutex` would have to go.

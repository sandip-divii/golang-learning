# ============================================================
# Stage 1 — BUILD: compile the Go binary inside a Go toolchain image
# ============================================================
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency manifests first and download modules as a separate
# layer. Docker caches layers: as long as go.mod/go.sum don't change,
# rebuilds skip the download entirely — only your code recompiles.
COPY go.mod go.sum ./
RUN go mod download

# Now copy the actual source and compile.
COPY . .

# CGO_ENABLED=0  -> pure-static binary, no C libraries needed at runtime
# -ldflags "-s -w" -> strip debug info, smaller binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /server ./cmd/server

# ============================================================
# Stage 2 — RUN: copy ONLY the binary into a tiny runtime image
# ============================================================
FROM alpine:3.20

# TLS root certificates + timezone data (good hygiene for any API)
RUN apk add --no-cache ca-certificates tzdata

# Never run containers as root
RUN adduser -D -u 10001 appuser
USER appuser

WORKDIR /app
COPY --from=builder /server .

# Documentation only — the actual port comes from the PORT env var
EXPOSE 8093

ENTRYPOINT ["./server"]

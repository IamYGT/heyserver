# ─── Stage 1: Build Frontend ─────────────────────────────────────────────────
FROM node:24-alpine AS frontend

WORKDIR /app/web

# Install deps first (better layer cache)
COPY web/package.json web/package-lock.json ./
RUN npm ci

# Copy the generated public API contract where the Vite build plugin expects it.
COPY docs/openapi.json /app/docs/openapi.json
COPY internal/api/routes_manifest.go /app/internal/api/routes_manifest.go

# Copy source and build
COPY web/ ./
RUN npm run build

# ─── Stage 2: Build Go Binary ────────────────────────────────────────────────
FROM golang:1.26-alpine AS backend

# gcc + musl-dev needed for CGO (sqlite3)
RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# Download modules first (better layer cache)
COPY go.mod go.sum ./
RUN go mod download

# Copy Go source
COPY cmd/    cmd/
COPY internal/ internal/

# Copy frontend dist into Go embed directory
COPY --from=frontend /app/web/dist/ cmd/hserver/web/dist/

# Build args for version injection
ARG VERSION=dev

RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags "-s -w -X github.com/IamYGT/heyserver/internal/config.Version=${VERSION}" \
    -o /binary/hserver-panel \
    ./cmd/hserver

# ─── Stage 3: Runtime ────────────────────────────────────────────────────────
FROM alpine:3.20

# CA certificates (HTTPS to external APIs: Cloudflare, GitHub, etc.)
# tzdata for correct timezone in logs
RUN apk add --no-cache ca-certificates tzdata && \
    update-ca-certificates

# Non-root user for security (panel itself still needs root on the actual server
# for nginx/php/systemd management, but this is for local dev/testing only)
RUN addgroup -S hserver && adduser -S hserver -G hserver

WORKDIR /app

# Copy compiled binary from builder
COPY --from=backend /binary/hserver-panel ./hserver-panel

# Data directory (override via volume mount in production)
RUN mkdir -p data && chown -R hserver:hserver /app

USER hserver

# Default environment (override via .env volume mount or environment variables)
ENV HSERVER_PORT=3085 \
    HSERVER_DB_PATH=/app/data/hserver.db \
    HSERVER_DATA_DIR=/app/data

EXPOSE 3085

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:3085/api/health || exit 1

ENTRYPOINT ["/app/hserver-panel"]

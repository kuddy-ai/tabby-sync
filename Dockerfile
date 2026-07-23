# syntax=docker/dockerfile:1

# --- Build stage ---
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-mod=readonly \
    go build -trimpath -ldflags='-s -w' -o /out/tabby-sync ./cmd/tabby-sync

# --- Runtime stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S tabby \
    && adduser -S -G tabby -h /home/tabby tabby

COPY --from=builder /out/tabby-sync /usr/local/bin/tabby-sync
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Data directory for SQLite DB, master key, and (auto-generated) users.yml
RUN mkdir -p /data && chown tabby:tabby /data
VOLUME ["/data"]

USER tabby

ENV TABBY_SYNC_DATA_DIR=/data \
    TABBY_SYNC_USERS_FILE=/data/users.yml \
    TABBY_SYNC_MASTER_KEY_PROVIDER=file

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:8080/healthz"]

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve"]

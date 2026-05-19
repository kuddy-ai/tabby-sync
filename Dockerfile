# syntax=docker/dockerfile:1

# --- Build stage ---
FROM golang:1.24-alpine AS builder

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

# Data directory for SQLite DB and master key
RUN mkdir -p /data && chown tabby:tabby /data
VOLUME ["/data"]

USER tabby

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:8080/healthz"]

ENTRYPOINT ["tabby-sync"]
CMD ["serve"]

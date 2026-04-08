# --- Stage 1: Builder ---
FROM golang:1.26.1-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata
WORKDIR /app

# Create the non-root user
RUN adduser -D -u 1000 wolf

COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o companion ./cmd/companion/main.go

# --- Stage 2: Final Image ---
FROM scratch

# Transfer user/group (so "USER wolf" works)
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Transfer SSL Certs (Essential for Cloudflare/NetBird)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Transfer Timezone info
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
ENV TZ=America/Montreal

# Transfer the binary
COPY --from=builder /app/companion /companion

USER wolf

# Ensure your Go code in cmd/companion/main.go handles a -health flag!
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/companion", "-health"]

ENTRYPOINT ["/companion"]
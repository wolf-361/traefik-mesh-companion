# --- Stage 1: Builder ---
FROM golang:1.26-alpine AS builder

# Install git and certificates (needed for HTTPS calls to APIs)
RUN apk add --no-cache git ca-certificates tzdata && update-ca-certificates
WORKDIR /app

# Copy and download dependencies first (leveraging Docker cache)
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary
# CGO_ENABLED=0 ensures a static binary that runs on 'scratch'
# -ldflags="-s -w" strips debug information to reduce size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o companion ./cmd/companion/main.go

# --- Stage 2: Final Image ---
FROM scratch

# Copy the timezone database from the builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy SSL certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /app/companion /companion

# Run as a non-privileged user for security
USER 1000:1000

# Call our own binary with the health flag every 30 seconds
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/companion", "-health"]

# Set the entrypoint
ENTRYPOINT ["/companion"]
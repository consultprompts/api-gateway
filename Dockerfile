# Stage 1 — builder
FROM golang:1.26.4-alpine AS builder

WORKDIR /app

# Copy dependency files first (cached as separate layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o api-gateway .

# Stage 2 — final image
FROM gcr.io/distroless/static-debian12

WORKDIR /

# Copy binary from builder
COPY --from=builder /app/api-gateway .

EXPOSE 8080

ENTRYPOINT ["/api-gateway"]

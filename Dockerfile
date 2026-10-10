# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 go build -o flashdb ./main.go

# Runtime stage
FROM alpine:latest

WORKDIR /root/

# Install netcat for healthcheck
RUN apk add --no-cache netcat-openbsd

# Copy binary from builder
COPY --from=builder /app/flashdb .

# Expose Redis port and metrics port
EXPOSE 6379 9121

# Run the application
CMD ["./flashdb"]

# Stage 1: Build the Go binary
FROM golang:1.26.1-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY . .
# Build a statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -o flag-service .

# Stage 2: Create the minimal production image
FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/flag-service .
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
RUN chown appuser:appgroup flag-service

USER appuser

EXPOSE 8080
CMD ["./flag-service"]
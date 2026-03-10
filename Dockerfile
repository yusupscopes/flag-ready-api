# syntax=docker/dockerfile:1

# Stage 1: Build the Go binary
FROM golang:1.26.1-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .
# Build a statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /flag-service

FROM builder AS tester
RUN go test -v ./...

# Stage 2: Create the minimal production image
FROM gcr.io/distroless/base-debian11 AS runner

WORKDIR /

COPY --from=builder /flag-service /flag-service

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/flag-service"]
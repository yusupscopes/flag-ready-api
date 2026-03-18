# Production-Ready Feature Flag System

[![Go Tests](https://github.com/yusupscopes/flag-ready-api/actions/workflows/ci.yml/badge.svg)](https://github.com/yusupscopes/flag-ready-api/actions/workflows/ci.yml)

A robust, highly available feature flag (toggle) microservice built in Go. This project demonstrates how modern engineering teams safely test and roll out new features using canary deployments, high-speed caching, and production-grade infrastructure patterns.

## 🚀 Key Features & Architecture

This system was engineered with production reliability in mind, solving real-world scale and deployment challenges:

- **Safe Canary Rollouts:** Implements deterministic hashing (`crc32`) to guarantee consistent, flicker-free percentage-based feature rollouts for users.
- **Write-Through Caching (Redis):** Serves flag evaluations in sub-milliseconds. Admin updates immediately refresh the Redis cache, ensuring 100% cache hits and zero stale data.
- **Database Migrations:** Uses `golang-migrate` to treat PostgreSQL database schemas as version-controlled code, preventing collision and corruption during startup.
- **Graceful Shutdown:** Intercepts `SIGTERM` signals to stop accepting new traffic, drain existing HTTP requests, and safely close database connections without dropping users.
- **Structured Observability:** Utilizes Go 1.21's `log/slog` to output machine-readable JSON logs, ready for ingestion by tools like Datadog or Splunk.
- **API Security:** Protects the administrative endpoints via custom API Key middleware.
- **Continuous Integration:** Automated GitHub Actions pipeline to run table-driven unit tests, verifying rollout math and edge cases on every push.

## 🛠️ Tech Stack

- **Language:** Go (Golang 1.22)
- **Database:** PostgreSQL (with `golang-migrate`)
- **Cache:** Redis
- **Containerization:** Docker & Docker Compose (Multi-stage builds)
- **CI/CD:** GitHub Actions

## 📦 Getting Started

### Prerequisites

Make sure you have [Docker](https://docs.docker.com/get-docker/) and Docker Compose installed on your machine.

### Running the System

1. Clone this repository.
2. Open your terminal in the project root.
3. Start the services using Docker Compose:
   ```bash
   docker-compose up --build
   ```
   The system will automatically boot the database, run the SQL migrations, connect to Redis, and start the Go API on port `3000`.

## 💻 API Usage

### 1. Evaluate a Flag (Public Endpoint)

Check if a feature is enabled for a specific user. The hashing algorithm ensures `user_123` always gets the exact same result for a specific feature.

**Request:**

```bash
curl "http://localhost:3000/flag?name=new_dashboard&user_id=user_123"
```

**Response:**

```json
{
  "feature": "new_dashboard",
  "enabled": true,
  "cached": true
}
```

### 2. Update a Flag (Protected Admin Endpoint)

Create or update a flag. This endpoint securely updates the Postgres database and actively refreshes the Redis cache via a Write-Through pattern. Requires an API Key.

**Request:**

```bash
curl -X POST http://localhost:3000/admin/flag \
     -H "Content-Type: application/json" \
     -H "Authorization: Bearer my-super-secure-production-key-999" \
     -d '{
           "feature": "beta_checkout",
           "enabled": true,
           "rollout_percentage": 50
         }'
```

**Response:**

```json
{
  "status": "success",
  "message": "Flag updated successfully"
}
```

## 🧪 Running Tests

To verify the deterministic hashing, percentage distribution math, and edge cases, run the standard Go test suite:

```bash
go test -v ./...
```

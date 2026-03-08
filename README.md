# Feature Flag System

[![Go Tests](https://github.com/yusupscopes/flag-ready-api/actions/workflows/ci.yml/badge.svg)](https://github.com/yusupscopes/flag-ready-api/actions/workflows/ci.yml)

A robust, production-ready feature flag (toggle) service built in Go. This project demonstrates how modern engineering teams safely test and roll out new features without deploying new code.

## 🚀 Key Features & Learning Outcomes

This project was built iteratively to cover essential backend engineering patterns:

- **Configuration Management:** Managing feature states dynamically.
- **Database Persistence (PostgreSQL):** Moving flags from ephemeral memory into a persistent, reliable source of truth.
- **Caching Layer (Redis):** Implementing the Cache-Aside pattern to serve flag checks in sub-milliseconds and protect the database from heavy read traffic.
- **Cache Invalidation:** Automatically clearing stale Redis data when a flag is updated via the Admin API.
- **Safe Feature Rollouts (Canary Releases):** Using deterministic hashing (`crc32`) to ensure consistent, flicker-free percentage-based rollouts (e.g., turning a feature on for exactly 30% of users).
- **Security & Middleware:** Protecting the Admin API with an API Key authentication middleware.
- **Production Deployment:** Utilizing multi-stage `Dockerfile` builds and `docker-compose` to orchestrate the API, Database, and Cache.
- **Testing:** Table-driven unit tests verifying edge cases and statistical distribution for the hashing logic.

## 🛠️ Tech Stack

- **Language:** Go (Golang)
- **Database:** PostgreSQL
- **Cache:** Redis
- **Containerization:** Docker & Docker Compose

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
   This will spin up the PostgreSQL database, the Redis cache, and the Go API on port `8080`.

## 💻 API Usage

### 1. Evaluate a Flag (Public Endpoint)

To check if a feature is enabled for a specific user, make a `GET` request to the `/flag` endpoint.

**Request:**

```bash
curl "http://localhost:8080/flag?name=new_dashboard&user_id=user_123"
```

**Response:**

```json
{
  "feature": "new_dashboard",
  "enabled": true,
  "cached": false
}
```

_(Note: If you repeat the request, `"cached"` will turn to `true` as the system serves the read from Redis!)_

### 2. Update a Flag (Protected Admin Endpoint)

To create or update a flag, send a `POST` request to the `/admin/flag` endpoint. You must include the `Authorization` header with your API key (default for local dev is `my-super-secure-production-key-999` as set in `docker-compose.yml`).

**Request:**

```bash
curl -X POST http://localhost:8080/admin/flag \
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

_(This will automatically update the Postgres database and invalidate the Redis cache for that specific feature.)_

## 🧪 Running Tests

To verify the deterministic hashing, percentage distribution math, and edge cases, run the standard Go test suite:

```bash
go test -v
```

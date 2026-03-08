package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

var (
	db  *sql.DB
	rdb *redis.Client
	ctx = context.Background() // Required by the Redis client
)

type FlagResponse struct {
	Feature string `json:"feature"`
	Enabled bool   `json:"enabled"`
	Cached  bool   `json:"cached"` // Let's add this so we can see when the cache works!
}

func main() {
	initDB()
	initRedis()
	defer db.Close()
	defer rdb.Close()

	http.HandleFunc("/flag", getFlagHandler)

	log.Println("Starting feature flag server on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func initRedis() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "localhost:6379"
	}

	rdb = redis.NewClient(&redis.Options{
		Addr: redisURL,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("Redis initialized successfully!")
}

func initDB() {
	// ... (Keep your exact initDB code from Phase 2 here) ...
	var err error
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://flag_user:supersecretpassword@localhost:5432/flag_db?sslmode=disable"
	}

	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to connect to DB: %v", err)
	}

	if err = db.Ping(); err != nil {
		log.Fatalf("Failed to ping DB: %v", err)
	}

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS feature_flags (
		name VARCHAR(255) PRIMARY KEY,
		enabled BOOLEAN NOT NULL
	);
	INSERT INTO feature_flags (name, enabled) VALUES ('new_dashboard', true) ON CONFLICT DO NOTHING;
	INSERT INTO feature_flags (name, enabled) VALUES ('beta_checkout', false) ON CONFLICT DO NOTHING;
	`
	_, err = db.Exec(createTableQuery)
	if err != nil {
		log.Fatalf("Failed to initialize database schema: %v", err)
	}
	log.Println("Database initialized successfully!")
}

func getFlagHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	featureName := r.URL.Query().Get("name")
	if featureName == "" {
		http.Error(w, "Missing 'name' query parameter", http.StatusBadRequest)
		return
	}

	// 1. TRY THE CACHE FIRST
	cachedValue, err := rdb.Get(ctx, featureName).Result()
	if err == nil {
		// Cache Hit! Redis stores everything as strings, so we parse it back to a boolean.
		isEnabled, _ := strconv.ParseBool(cachedValue)
		respond(w, featureName, isEnabled, true)
		return
	}

	// 2. CACHE MISS: QUERY THE DATABASE
	log.Printf("Cache miss for %s, querying database...", featureName)
	var isEnabled bool
	err = db.QueryRow("SELECT enabled FROM feature_flags WHERE name = $1", featureName).Scan(&isEnabled)
	
	if err != nil {
		if err == sql.ErrNoRows {
			isEnabled = false
		} else {
			log.Printf("Database error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	// 3. SAVE TO CACHE FOR NEXT TIME
	// We set an expiration (TTL) of 5 minutes. If we change the flag in the DB, 
	// it will take up to 5 minutes to reflect in the cache.
	rdb.Set(ctx, featureName, isEnabled, 5*time.Minute)

	// 4. RESPOND
	respond(w, featureName, isEnabled, false)
}

// Helper function to send the JSON response
func respond(w http.ResponseWriter, feature string, enabled bool, cached bool) {
	response := FlagResponse{
		Feature: feature,
		Enabled: enabled,
		Cached:  cached,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
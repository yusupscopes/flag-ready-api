package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
	"hash/crc32"

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

// We create a tiny struct just to help us store the DB config in Redis as JSON
type FeatureConfig struct {
	Enabled    bool `json:"enabled"`
	Percentage int  `json:"percentage"`
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
		enabled BOOLEAN NOT NULL,
		rollout_percentage INT DEFAULT 100
	);
	-- Notice we added a 50% rollout for the new_dashboard
	INSERT INTO feature_flags (name, enabled, rollout_percentage) VALUES ('new_dashboard', true, 50) ON CONFLICT DO NOTHING;
	INSERT INTO feature_flags (name, enabled, rollout_percentage) VALUES ('beta_checkout', false, 0) ON CONFLICT DO NOTHING;
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
	userID := r.URL.Query().Get("user_id")

	if featureName == "" || userID == "" {
		http.Error(w, "Missing 'name' or 'user_id' query parameter", http.StatusBadRequest)
		return
	}

	var config FeatureConfig
	isCached := false

	// 1. TRY THE CACHE FIRST
	cachedValue, err := rdb.Get(ctx, featureName).Result()
	if err == nil {
		// CACHE HIT: Parse the JSON string from Redis back into our struct
		json.Unmarshal([]byte(cachedValue), &config)
		isCached = true
	} else {
		// 2. CACHE MISS: QUERY THE DATABASE
		log.Printf("Cache miss for %s, querying database...", featureName)
		
		err = db.QueryRow("SELECT enabled, rollout_percentage FROM feature_flags WHERE name = $1", featureName).Scan(&config.Enabled, &config.Percentage)
		
		if err != nil {
			if err == sql.ErrNoRows {
				config.Enabled = false
				config.Percentage = 0
			} else {
				log.Printf("Database error: %v", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
		}

		// 3. SAVE TO CACHE FOR NEXT TIME
		// Convert the struct to JSON and store it in Redis for 5 minutes
		configBytes, _ := json.Marshal(config)
		rdb.Set(ctx, featureName, configBytes, 5*time.Minute)
	}

	// 4. CALCULATE FINAL STATE IN MEMORY
	// Now that we have the config (from either Cache or DB), calculate the rollout
	finalState := false
	if config.Enabled {
		finalState = isUserInRollout(userID, featureName, config.Percentage)
	}

	// 5. RESPOND
	respond(w, featureName, finalState, isCached)
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

// isUserInRollout calculates if a specific user should see a feature
func isUserInRollout(userID string, featureName string, percentage int) bool {
	if percentage == 0 {
		return false
	}
	if percentage == 100 {
		return true
	}

	// Combine user ID and feature name. 
	// This ensures User A might get feature X, but not necessarily feature Y.
	hashInput := userID + "-" + featureName
	
	// Generate a consistent numeric hash
	hash := crc32.ChecksumIEEE([]byte(hashInput))

	// Modulo 100 gives us a number from 0-99. We add 1 to get 1-100.
	bucket := (int(hash) % 100) + 1

	return bucket <= percentage
}
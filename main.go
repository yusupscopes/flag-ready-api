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

// UpdateFlagRequest is the JSON structure we expect for updates
type UpdateFlagRequest struct {
	Feature           string `json:"feature"`
	Enabled           bool   `json:"enabled"`
	RolloutPercentage int    `json:"rollout_percentage"`
}

func main() {
	initDB()
	initRedis()
	defer db.Close()
	defer rdb.Close()

	// Public route (anyone can read flags)
	http.HandleFunc("/flag", getFlagHandler)

	// Protected admin route (only users with the API key can update flags)
	http.HandleFunc("/admin/flag", adminAuthMiddleware(updateFlagHandler))

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

func updateFlagHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UpdateFlagRequest
	// Decode the incoming JSON body
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Feature == "" {
		http.Error(w, "Feature name is required", http.StatusBadRequest)
		return
	}

	// 1. UPDATE THE DATABASE
	// We use an UPSERT (INSERT ... ON CONFLICT DO UPDATE) so this endpoint can 
	// both create new flags and update existing ones.
	query := `
		INSERT INTO feature_flags (name, enabled, rollout_percentage) 
		VALUES ($1, $2, $3)
		ON CONFLICT (name) 
		DO UPDATE SET enabled = EXCLUDED.enabled, rollout_percentage = EXCLUDED.rollout_percentage;
	`
	_, err := db.Exec(query, req.Feature, req.Enabled, req.RolloutPercentage)
	if err != nil {
		log.Printf("Database update error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// 2. INVALIDATE THE CACHE (Crucial Step!)
	// Instead of deleting the key, we actively save the new JSON config to Redis
	newConfig := FeatureConfig{
		Enabled:    req.Enabled,
		Percentage: req.RolloutPercentage,
	}
	configBytes, _ := json.Marshal(newConfig)

	err = rdb.Set(ctx, req.Feature, configBytes, 5*time.Minute).Err()
	if err != nil {
		log.Printf("Failed to update cache for %s: %v", req.Feature, err)
		// We log the error but still return success since the DB updated correctly
	}

	log.Printf("Flag updated & cache refreshed: %s (Enabled: %v, Rollout: %d%%)", req.Feature, req.Enabled, req.RolloutPercentage)

	// 3. RESPOND WITH SUCCESS
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success","message":"Flag updated successfully"}`))
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

// adminAuthMiddleware intercepts requests to ensure the user has the correct API key
func adminAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// We'll read the expected key from the environment variables
		expectedKey := os.Getenv("ADMIN_API_KEY")
		if expectedKey == "" {
			// A fallback for local development if we forget to set the env var
			expectedKey = "dev-secret-key"
		}

		// The standard way to pass API keys is in the "Authorization" header
		// Format: "Bearer YOUR_API_KEY"
		authHeader := r.Header.Get("Authorization")
		expectedHeader := "Bearer " + expectedKey

		// If the keys don't match, block the request immediately with a 401 Unauthorized
		if authHeader != expectedHeader {
			log.Printf("Unauthorized access attempt to admin API from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized: Invalid or missing API Key", http.StatusUnauthorized)
			return
		}

		// If the key is correct, pass the request on to the actual handler (updateFlagHandler)
		next(w, r)
	}
}
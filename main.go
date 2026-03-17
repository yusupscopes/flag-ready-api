package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"hash/crc32"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
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

    // 1. Setup our router
	mux := http.NewServeMux()
	// Public route (anyone can read flags)
	mux.HandleFunc("/flag", getFlagHandler)
	// Protected admin route (only users with the API key can update flags)
	mux.HandleFunc("/admin/flag", adminAuthMiddleware(updateFlagHandler))
	// Protected admin route: list all features and their status (for dashboard)
	mux.HandleFunc("/admin/features", adminAuthMiddleware(listFeaturesHandler))

	// 2. Create a custom HTTP server instance
	srv := &http.Server{
		Addr: ":3000",
		Handler: corsMiddleware(mux),
	}

	// 3. Create a channel to listen for OS signals (like Ctrl+C or Docker stop)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	// 4. Start the server in a separate Goroutine (background thread)
	// This allows the main thread to keep moving forward to the 'wait' block.
	go func() {
		log.Println("Starting feature flag server on :3000...")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// 5. Block the main thread here until a signal comes down the channel
	<-stopChan
	log.Println("\nShutdown signal received. Shutting down gracefully...")

	// 6. Create a deadline context. We give the server 10 seconds to finish 
	// current requests before we forcefully kill it.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 7. Tell the server to stop accepting new requests and finish existing ones
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown abruptly: %v", err)
	}

	// 8. Safely close our persistent connections
	log.Println("Closing database and Redis connections...")
	db.Close()
	rdb.Close()

	log.Println("Server exited cleanly. Goodbye!")
}

// corsMiddleware sets CORS headers on all API responses and handles OPTIONS preflight.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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

	log.Println("Database connected. Running migrations...")

	// Initialize the Postgres driver for the migration engine
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		log.Fatalf("Could not start sql migration driver: %v", err)
	}

	// Tell the engine where to find our .sql files (the "file://migrations" folder)
	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations",
		"postgres", driver)
	if err != nil {
		log.Fatalf("Migration initialization failed: %v", err)
	}

	// Run the UP migrations
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("Migration failed to apply: %v", err)
	}

	log.Println("Database migrated successfully!")
}

func getFlagHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqCtx := r.Context()

	featureName := r.URL.Query().Get("name")
	userID := r.URL.Query().Get("user_id")

	if featureName == "" || userID == "" {
		http.Error(w, "Missing 'name' or 'user_id' query parameter", http.StatusBadRequest)
		return
	}

	var config FeatureConfig
	isCached := false

	// 1. TRY THE CACHE FIRST
	cachedValue, err := rdb.Get(reqCtx, featureName).Result()
	if err == nil {
		// CACHE HIT: Parse the JSON string from Redis back into our struct
		json.Unmarshal([]byte(cachedValue), &config)
		isCached = true
	} else {
		// 2. CACHE MISS: QUERY THE DATABASE
		log.Printf("Cache miss for %s, querying database...", featureName)
		
		err = db.QueryRowContext(reqCtx, "SELECT enabled, rollout_percentage FROM feature_flags WHERE name = $1", featureName).Scan(&config.Enabled, &config.Percentage)
		
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
		rdb.Set(reqCtx, featureName, configBytes, 5*time.Minute)
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

	reqCtx := r.Context()

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
	_, err := db.ExecContext(reqCtx, query, req.Feature, req.Enabled, req.RolloutPercentage)
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

	err = rdb.Set(reqCtx, req.Feature, configBytes, 5*time.Minute).Err()
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

// listFeaturesHandler returns all feature flags (name, enabled) for the dashboard. No Redis; list is from DB.
func listFeaturesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqCtx := r.Context()

	rows, err := db.QueryContext(reqCtx, "SELECT name, enabled FROM feature_flags ORDER BY name")
	if err != nil {
		log.Printf("Database list error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []FlagResponse
	for rows.Next() {
		var name string
		var enabled bool
		if err := rows.Scan(&name, &enabled); err != nil {
			log.Printf("Database scan error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		list = append(list, FlagResponse{Feature: name, Enabled: enabled, Cached: false})
	}
	if err := rows.Err(); err != nil {
		log.Printf("Database rows error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if list == nil {
		list = []FlagResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
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
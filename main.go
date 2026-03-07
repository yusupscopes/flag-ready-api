package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq" // Import the Postgres driver
)

var db *sql.DB

type FlagResponse struct {
	Feature string `json:"feature"`
	Enabled bool   `json:"enabled"`
}

func main() {
	initDB()
	defer db.Close()

	http.HandleFunc("/flag", getFlagHandler)

	log.Println("Starting feature flag server on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func initDB() {
	var err error
	// Read the connection string from the environment variable set by Docker Compose
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		// Fallback for local testing outside of Docker
		connStr = "postgres://flag_user:supersecretpassword@localhost:5432/flag_db?sslmode=disable"
	}

	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to connect to DB: %v", err)
	}

	if err = db.Ping(); err != nil {
		log.Fatalf("Failed to ping DB: %v", err)
	}

	// For learning purposes, we'll auto-create the table and insert a dummy row.
	// In a real app, you'd use a database migration tool (like golang-migrate).
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

	var isEnabled bool
	// Query the database for the specific flag
	err := db.QueryRow("SELECT enabled FROM feature_flags WHERE name = $1", featureName).Scan(&isEnabled)
	
	if err != nil {
		if err == sql.ErrNoRows {
			// If the flag doesn't exist, we generally default to false to be safe
			isEnabled = false 
		} else {
			log.Printf("Database error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	response := FlagResponse{
		Feature: featureName,
		Enabled: isEnabled,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
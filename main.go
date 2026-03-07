package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
)

// FlagStore holds our feature flags in memory safely
type FlagStore struct {
	sync.RWMutex
	flags map[string]bool
}

var store = &FlagStore{
	flags: make(map[string]bool),
}

// FlagResponse is the JSON structure we return
type FlagResponse struct {
	Feature string `json:"feature"`
	Enabled bool   `json:"enabled"`
}

func main() {
	// Seed some initial data
	store.flags["new_dashboard"] = true
	store.flags["beta_checkout"] = false

	// Define our HTTP routes
	http.HandleFunc("/flag", getFlagHandler)

	log.Println("Starting feature flag server on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

// getFlagHandler handles GET /flag?name=feature_name
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

	// Safely read from the map
	store.RLock()
	isEnabled := store.flags[featureName]
	store.RUnlock()

	response := FlagResponse{
		Feature: featureName,
		Enabled: isEnabled,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
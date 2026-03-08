package main

import (
	"fmt"
	"testing"
)

// Test 1: Table-Driven Test for Edge Cases
func TestIsUserInRollout_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		feature    string
		percentage int
		expected   bool
	}{
		{"0 percent is always false", "user_123", "new_dashboard", 0, false},
		{"100 percent is always true", "user_123", "new_dashboard", 100, true},
		{"0 percent is false for different user", "user_999", "new_dashboard", 0, false},
		{"100 percent is true for different user", "user_999", "new_dashboard", 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isUserInRollout(tt.userID, tt.feature, tt.percentage)
			if result != tt.expected {
				t.Errorf("got %v, expected %v", result, tt.expected)
			}
		})
	}
}

// Test 2: Verify Consistent Hashing (No Flickering)
func TestIsUserInRollout_Consistency(t *testing.T) {
	userID := "user_42"
	featureName := "beta_checkout"
	percentage := 50

	// Get the initial result
	firstResult := isUserInRollout(userID, featureName, percentage)

	// Run it 100 more times. It should NEVER change.
	for i := 0; i < 100; i++ {
		if isUserInRollout(userID, featureName, percentage) != firstResult {
			t.Errorf("Inconsistent result detected! Hashing is not deterministic.")
		}
	}
}

// Test 3: Check the Statistical Distribution
func TestIsUserInRollout_Distribution(t *testing.T) {
	featureName := "dark_mode"
	targetPercentage := 30
	totalUsers := 100000

	enabledCount := 0

	// Simulate 100,000 distinct users
	for i := 0; i < totalUsers; i++ {
		userID := fmt.Sprintf("user_%d", i)
		if isUserInRollout(userID, featureName, targetPercentage) {
			enabledCount++
		}
	}

	// Calculate actual percentage
	actualPercentage := (float64(enabledCount) / float64(totalUsers)) * 100.0

	// We expect the actual percentage to be very close to our 30% target.
	// We'll allow a small margin of error (e.g., +/- 1%).
	marginOfError := 1.0

	if actualPercentage < (float64(targetPercentage)-marginOfError) || actualPercentage > (float64(targetPercentage)+marginOfError) {
		t.Errorf("Expected distribution around %d%%, but got %.2f%%", targetPercentage, actualPercentage)
	} else {
		t.Logf("Success: Target was %d%%, Actual was %.2f%%", targetPercentage, actualPercentage)
	}
}
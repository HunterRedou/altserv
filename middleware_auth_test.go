package main

import (
	"testing"
	"time"
)

func TestSessionTTL(t *testing.T) {
	if got := (&apiConfig{}).sessionTTL(); got != sessionDuration {
		t.Errorf("default sessionTTL = %s, want %s", got, sessionDuration)
	}

	if got := (&apiConfig{sessionDuration: 2 * time.Hour}).sessionTTL(); got != 2*time.Hour {
		t.Errorf("configured sessionTTL = %s, want 2h", got)
	}
}

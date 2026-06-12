package config

import (
	"testing"
	"time"
)

func TestLoadResultsDefaults(t *testing.T) {
	// No BETHOVEN_RESULTS_* set (t.Setenv is not used, so they're absent under
	// `go test`'s clean-ish env; explicitly clear to be safe).
	t.Setenv("BETHOVEN_RESULTS_ENABLED", "")
	t.Setenv("BETHOVEN_RESULTS_API_KEY", "")
	t.Setenv("BETHOVEN_RESULTS_COMPETITION", "")
	t.Setenv("BETHOVEN_RESULTS_INTERVAL", "")

	cfg := Load()
	if cfg.Results.Enabled {
		t.Error("results should be disabled by default")
	}
	if cfg.Results.Competition != DefaultResultsCompetition {
		t.Errorf("competition default = %q, want %q", cfg.Results.Competition, DefaultResultsCompetition)
	}
	if cfg.Results.Interval != DefaultResultsInterval {
		t.Errorf("interval default = %s, want %s", cfg.Results.Interval, DefaultResultsInterval)
	}
}

func TestLoadResultsFromEnv(t *testing.T) {
	t.Setenv("BETHOVEN_RESULTS_ENABLED", "true")
	t.Setenv("BETHOVEN_RESULTS_API_KEY", "abc123")
	t.Setenv("BETHOVEN_RESULTS_COMPETITION", "CL")
	t.Setenv("BETHOVEN_RESULTS_INTERVAL", "90s")

	cfg := Load()
	if !cfg.Results.Enabled {
		t.Error("expected results enabled")
	}
	if cfg.Results.APIKey != "abc123" {
		t.Errorf("api key = %q", cfg.Results.APIKey)
	}
	if cfg.Results.Competition != "CL" {
		t.Errorf("competition = %q, want CL", cfg.Results.Competition)
	}
	if cfg.Results.Interval != 90*time.Second {
		t.Errorf("interval = %s, want 90s", cfg.Results.Interval)
	}
}

func TestLoadResultsIntervalFallsBackOnGarbage(t *testing.T) {
	t.Setenv("BETHOVEN_RESULTS_INTERVAL", "not-a-duration")
	cfg := Load()
	if cfg.Results.Interval != DefaultResultsInterval {
		t.Errorf("bad interval should fall back to %s, got %s", DefaultResultsInterval, cfg.Results.Interval)
	}
}

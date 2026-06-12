// Package config loads BEThoven's runtime configuration from environment
// variables, applying sensible defaults for local development.
package config

import (
	"log"
	"os"
	"strings"
	"time"
)

// DefaultInviteCode is the dev fallback used by `make run`. Running a real
// deployment with this is unsafe — see Config.UsingDefaultInvite.
const DefaultInviteCode = "letmein"

// DefaultTimezone is the IANA zone used to display kickoff/result times when
// BETHOVEN_TIMEZONE is unset. The reference deployment is a Brazilian pool.
const DefaultTimezone = "America/Sao_Paulo"

// Results defaults: ESPN's World Cup league slug and a gentle poll cadence
// (the feed is final-only, so there's no reason to hammer it).
const (
	DefaultResultsLeague   = "fifa.world"
	DefaultResultsInterval = 5 * time.Minute
)

// Config holds everything the server needs to boot. Values come from
// BETHOVEN_* environment variables; see Load for defaults.
type Config struct {
	Port        string   // TCP port the SSH server listens on
	DBPath      string   // SQLite database file
	HostKeyPath string   // persistent SSH host key (generated if absent)
	InviteCode  string   // shared secret required on first connect
	Admins      []string // SHA256 fingerprints granted the admin role
	Timezone    string   // IANA zone for displaying times (e.g. America/Sao_Paulo)
	Results     Results  // automatic results-feed polling
}

// Results configures the background poller that pulls finished match results
// from ESPN's public scoreboard. Disabled by default: the server ships dormant
// and a real deployment opts in by setting BETHOVEN_RESULTS_ENABLED. No API key
// — the ESPN endpoint is keyless.
type Results struct {
	Enabled  bool          // BETHOVEN_RESULTS_ENABLED
	League   string        // BETHOVEN_RESULTS_LEAGUE (ESPN slug, default "fifa.world")
	Interval time.Duration // BETHOVEN_RESULTS_INTERVAL (default 5m)
}

// UsingDefaultInvite reports whether the publicly-known dev invite code is in
// use alongside a configured admin — a strong signal of a real, misconfigured
// deployment that should set BETHOVEN_INVITE_CODE.
func (c Config) UsingDefaultInvite() bool {
	return c.InviteCode == DefaultInviteCode && len(c.Admins) > 0
}

// Load reads configuration from the environment. Defaults target a local
// dev run; production sets the BETHOVEN_* vars explicitly (see deploy docs).
func Load() Config {
	return Config{
		Port:        env("BETHOVEN_PORT", "2222"),
		DBPath:      env("BETHOVEN_DB_PATH", "bethoven.db"),
		HostKeyPath: env("BETHOVEN_HOST_KEY_PATH", "host_key"),
		InviteCode:  env("BETHOVEN_INVITE_CODE", DefaultInviteCode),
		Admins:      splitList(env("BETHOVEN_ADMINS", "")),
		Timezone:    env("BETHOVEN_TIMEZONE", DefaultTimezone),
		Results: Results{
			Enabled:  env("BETHOVEN_RESULTS_ENABLED", "") == "true",
			League:   env("BETHOVEN_RESULTS_LEAGUE", DefaultResultsLeague),
			Interval: envDuration("BETHOVEN_RESULTS_INTERVAL", DefaultResultsInterval),
		},
	}
}

// envDuration parses a Go duration string (e.g. "5m", "90s") from the
// environment, falling back to the default on an unset or unparseable value.
func envDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("WARNING: %s %q is not a valid duration (%v); using %s", key, raw, err, fallback)
		return fallback
	}
	return d
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitList parses a comma-separated env value into a trimmed, non-empty slice.
func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

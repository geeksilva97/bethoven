// Package config loads BEThoven's runtime configuration from environment
// variables, applying sensible defaults for local development.
package config

import (
	"os"
	"strconv"
	"strings"
)

// DefaultInviteCode is the dev fallback used by `make run`. Running a real
// deployment with this is unsafe — see Config.UsingDefaultInvite.
const DefaultInviteCode = "letmein"

// DefaultTimezone is the IANA zone used to display kickoff/result times when
// BETHOVEN_TIMEZONE is unset. The reference deployment is a Brazilian pool.
const DefaultTimezone = "America/Sao_Paulo"

// DefaultLiveLeague is ESPN's slug for the competition the live feed polls.
const DefaultLiveLeague = "fifa.world"

// DefaultLivePollSeconds is the gap between live-feed polls.
const DefaultLivePollSeconds = 60

// Config holds everything the server needs to boot. Values come from
// BETHOVEN_* environment variables; see Load for defaults.
type Config struct {
	Port        string   // TCP port the SSH server listens on
	DBPath      string   // SQLite database file
	HostKeyPath string   // persistent SSH host key (generated if absent)
	InviteCode  string   // shared secret required on first connect
	Admins      []string // SHA256 fingerprints granted the admin role
	Timezone    string   // IANA zone for displaying times (e.g. America/Sao_Paulo)

	// Live feed (optional). LiveEnabled gates the background score poller.
	LiveEnabled     bool
	LiveLeague      string // ESPN league slug, e.g. fifa.world
	LivePollSeconds int    // seconds between polls
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

		LiveEnabled:     env("BETHOVEN_LIVE_ENABLED", "true") != "false",
		LiveLeague:      env("BETHOVEN_LIVE_LEAGUE", DefaultLiveLeague),
		LivePollSeconds: envInt("BETHOVEN_LIVE_POLL_SECONDS", DefaultLivePollSeconds),
	}
}

// envInt reads a positive integer env var, falling back on the default when
// unset or unparseable.
func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
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

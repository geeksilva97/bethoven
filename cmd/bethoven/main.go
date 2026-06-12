// Command bethoven runs the World Cup prediction pool as an SSH server.
// Players connect with `ssh` and place picks in a terminal UI; their public
// key is their identity.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA tz database so BETHOVEN_TIMEZONE works on any host

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	"github.com/muesli/termenv"

	"bethoven/internal/clock"
	"bethoven/internal/config"
	"bethoven/internal/db"
	"bethoven/internal/results"
	"bethoven/internal/results/espn"
	"bethoven/internal/server"
	"bethoven/internal/service"
	"bethoven/internal/tui"
)

func main() {
	// Admin subcommand: place a bet on a player's behalf (bypasses the kickoff
	// lock). Handled before any server setup. See placebet.go.
	if len(os.Args) > 1 && os.Args[1] == "place-bet" {
		runPlaceBet(os.Args[2:])
		return
	}

	// Diagnostic subcommand: probe the results feed and print what we'd get,
	// without touching the database or starting the server. See checkfeed.go.
	if len(os.Args) > 1 && os.Args[1] == "check-feed" {
		runCheckFeed(os.Args[2:])
		return
	}

	// Force a color profile for lipgloss's global renderer. Under systemd the
	// process has no TTY/$TERM, so lipgloss would otherwise detect "no color"
	// and strip every style server-side — before the output is ever sent to the
	// client. Our clients are interactive SSH terminals, so pin ANSI256 (widely
	// supported; the gold accent downsamples cleanly). See styles.go.
	lipgloss.SetColorProfile(termenv.ANSI256)

	cfg := config.Load()
	if cfg.UsingDefaultInvite() {
		log.Println("WARNING: running with the default invite code while admins are configured — " +
			"set BETHOVEN_INVITE_CODE to a private value before sharing the address")
	}

	// Times are stored/locked in UTC but displayed in the configured zone.
	if loc, err := time.LoadLocation(cfg.Timezone); err != nil {
		log.Printf("WARNING: BETHOVEN_TIMEZONE %q not found (%v); displaying times in UTC", cfg.Timezone, err)
	} else {
		tui.SetLocation(loc)
	}

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	store := db.NewStore(conn)

	// Seed the group stage from fixtures.json on first boot (idempotent).
	now := time.Now().UTC()
	tournamentID := seed(store, now)

	svc := service.New(store, clock.Real{}, cfg.InviteCode, cfg.Admins, tournamentID)

	addr := net.JoinHostPort("", cfg.Port)
	srv, err := server.New(svc, addr, cfg.HostKeyPath)
	if err != nil {
		log.Fatalf("build server: %v", err)
	}

	ln, err := server.LimitedListen(addr, server.MaxConcurrentConns)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	// Background results poller (opt-in). Its context is cancelled on shutdown so
	// the goroutine exits cleanly with the server.
	pollCtx, stopPoller := context.WithCancel(context.Background())
	defer stopPoller()
	startResultsPoller(pollCtx, cfg.Results, svc)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("BEThoven listening on %s (ssh -p %s ...)", addr, cfg.Port)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")
	stopPoller()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// startResultsPoller launches the background results poller if enabled. The
// ESPN feed needs no API key, so enabling it is all the configuration there is.
func startResultsPoller(ctx context.Context, cfg config.Results, svc *service.Service) {
	if !cfg.Enabled {
		return
	}
	fetcher := espn.New(cfg.League, "")
	log.Printf("results: auto-update enabled (league %s, every %s)", cfg.League, cfg.Interval)
	go results.RunPoller(ctx, fetcher, svc, cfg.Interval)
}

// seed loads fixtures.json (if present) into an empty tournament and returns
// the active tournament id. A missing file is fine — the admin can add every
// match via the TUI instead.
func seed(store *db.Store, now time.Time) int64 {
	raw, err := os.ReadFile("fixtures.json")
	if err != nil {
		log.Printf("no fixtures.json (%v); starting empty — add matches via the admin TUI", err)
		// Ensure an active tournament still exists.
		raw = []byte(`{"tournament":"World Cup 2026","matches":[]}`)
	}
	tid, seeded, err := store.EnsureSeeded(raw, now)
	if err != nil {
		log.Fatalf("seed: %v", err)
	}
	if seeded {
		log.Println("seeded group-stage fixtures from fixtures.json")
	}
	return tid
}

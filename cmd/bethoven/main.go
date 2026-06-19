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

	"bethoven/internal/ai"
	"bethoven/internal/analytics"
	"bethoven/internal/clock"
	"bethoven/internal/config"
	"bethoven/internal/db"
	"bethoven/internal/live"
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

	// Admin subcommand: onboard BETanIA — create the AI player and seed past games
	// (web search off). One-time; see ai_seed.go.
	if len(os.Args) > 1 && os.Args[1] == "ai-seed" {
		runAISeed(os.Args[2:])
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
	tournamentID, teamForms := seed(store, now)

	svc := service.New(store, clock.Real{}, cfg.InviteCode, cfg.Admins, tournamentID)
	svc.SetTeamForms(teamForms) // pre-tournament recent-form baselines for the bet screen

	// Optional analytics: a SEPARATE database written asynchronously, so it can
	// never block or fail a bet. Disabled => no DB opened, behaviour unchanged.
	if cfg.AnalyticsEnabled {
		aconn, err := analytics.Open(cfg.AnalyticsDBPath)
		if err != nil {
			log.Fatalf("open analytics db: %v", err)
		}
		defer aconn.Close()
		rec := analytics.NewRecorder(analytics.NewStore(aconn))
		svc.SetAnalyticsSink(rec)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rec.Close(ctx); err != nil {
				log.Printf("analytics: drain on shutdown: %v", err)
			}
		}()
		log.Printf("analytics enabled (db=%s)", cfg.AnalyticsDBPath)
	}

	// Background context for long-running workers (the live poller); cancelled on
	// shutdown so they stop cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Optional live-score feed: poll ESPN's keyless scoreboard and fold the
	// results into the leaderboard. Disabled => the server runs exactly as before.
	if cfg.LiveEnabled {
		cache := live.NewCache()
		svc.SetLiveStore(cache)
		interval := time.Duration(cfg.LivePollSeconds) * time.Second
		poller := live.NewPoller(
			live.NewESPNProvider(cfg.LiveLeague),
			cache, svc.Fixtures, svc.FinalizeFromFeed, clock.Real{}, interval,
		)
		go poller.Run(ctx)
		log.Printf("live feed enabled (league=%s, every %s)", cfg.LiveLeague, interval)
	}

	// Optional AI player (BETanIA): a background worker that researches and bets
	// UPCOMING matches through the service (kickoff lock fully applies). Onboarding
	// (user creation + the historical seed) is the separate `bethoven ai-seed`
	// subcommand — here we only RESOLVE the already-created player. Disabled => no
	// worker, no SDK calls, behaviour identical to before.
	if cfg.AIEnabled {
		switch {
		case os.Getenv("ANTHROPIC_API_KEY") == "":
			log.Println("WARNING: BETHOVEN_AI_ENABLED but ANTHROPIC_API_KEY unset — BETanIA disabled")
		default:
			u, err := svc.Lookup(ai.Fingerprint)
			if err != nil {
				log.Println("WARNING: BETanIA not onboarded — run `bethoven ai-seed` first; live betting disabled")
				break
			}
			interval := time.Duration(cfg.AIIntervalSecs) * time.Second
			mon := ai.NewMonitor(cfg.AIModel, interval)
			svc.SetAIMonitor(mon)
			bettor := ai.NewBettor(ai.Deps{
				Fixtures: svc.Fixtures,
				MyBets:   svc.MyBets,
				PlaceBet: svc.PlaceBet,
				Now:      svc.Now,
			}, ai.NewAnthropicPredictor(cfg.AIModel, true), mon, u.ID, interval, cfg.AILogPath, cfg.AIMaxPerRun,
				time.Duration(cfg.AILookaheadHrs)*time.Hour)
			svc.SetAITrigger(bettor.Trigger)
			go bettor.Run(ctx)
			log.Printf("BETanIA live betting enabled (model=%s, every %ds, lookahead %dh)", cfg.AIModel, cfg.AIIntervalSecs, cfg.AILookaheadHrs)
		}
	}

	addr := net.JoinHostPort("", cfg.Port)
	srv, err := server.New(svc, addr, cfg.HostKeyPath)
	if err != nil {
		log.Fatalf("build server: %v", err)
	}

	ln, err := server.LimitedListen(addr, server.MaxConcurrentConns)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

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
	cancel() // stop the live poller
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// seed loads fixtures.json (if present) into an empty tournament and returns the
// active tournament id plus the per-team recent-form baselines (name→W/D/L). A
// missing file is fine — the admin can add every match via the TUI instead.
func seed(store *db.Store, now time.Time) (int64, map[string]string) {
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
	forms, err := db.ParseTeamForms(raw)
	if err != nil {
		log.Printf("WARNING: parse team forms: %v; bet screen will show form from results only", err)
	}
	return tid, forms
}

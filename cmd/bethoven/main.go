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

	"github.com/charmbracelet/ssh"

	"bethoven/internal/clock"
	"bethoven/internal/config"
	"bethoven/internal/db"
	"bethoven/internal/server"
	"bethoven/internal/service"
)

func main() {
	cfg := config.Load()

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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
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

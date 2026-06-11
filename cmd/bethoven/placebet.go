package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"bethoven/internal/config"
	"bethoven/internal/db"
	"bethoven/internal/models"
)

// runPlaceBet is the admin escape hatch: place or update a bet on a player's
// behalf, bypassing the kickoff lock. For helping someone who couldn't bet in
// time (e.g. a timezone mix-up). Writes straight to the store — NOT through
// service.PlaceBet — so it is intentionally not gated by the kickoff lock.
//
// Usage: bethoven place-bet "<display name>" "<team A>" "<team B>" <a> <b>
func runPlaceBet(args []string) {
	if len(args) != 5 {
		log.Fatalf(`usage: bethoven place-bet "<display name>" "<team A>" "<team B>" <scoreA> <scoreB>`)
	}
	name, teamA, teamB := args[0], args[1], args[2]
	predA, errA := strconv.Atoi(args[3])
	predB, errB := strconv.Atoi(args[4])
	if errA != nil || errB != nil || predA < 0 || predB < 0 {
		log.Fatalf("scores must be non-negative whole numbers, got %q and %q", args[3], args[4])
	}

	cfg := config.Load()
	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db %s: %v", cfg.DBPath, err)
	}
	defer conn.Close()
	store := db.NewStore(conn)

	t, err := store.ActiveTournament()
	if err != nil {
		log.Fatalf("active tournament: %v", err)
	}

	user := findUser(store, name)
	match := findMatch(store, t.ID, teamA, teamB)

	if err := store.UpsertBet(models.Bet{
		UserID:  user.ID,
		MatchID: match.ID,
		PredA:   predA,
		PredB:   predB,
	}, time.Now().UTC()); err != nil {
		log.Fatalf("upsert bet: %v", err)
	}
	fmt.Printf("placed: %s — %s %d-%d %s (match %d)\n",
		user.DisplayName, match.TeamA, predA, predB, match.TeamB, match.ID)
}

// findUser resolves a player by display name (case-insensitive), fatal if not found.
func findUser(store *db.Store, name string) models.User {
	users, err := store.AllUsers()
	if err != nil {
		log.Fatalf("list users: %v", err)
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, u := range users {
		if strings.ToLower(u.DisplayName) == want {
			return u
		}
	}
	log.Fatalf("no player named %q; known players: %s", name, userNames(users))
	return models.User{}
}

// findMatch resolves a fixture by both team names (case-insensitive), fatal if not found.
func findMatch(store *db.Store, tournamentID int64, teamA, teamB string) models.Match {
	matches, err := store.ListMatches(tournamentID)
	if err != nil {
		log.Fatalf("list matches: %v", err)
	}
	a, b := strings.ToLower(strings.TrimSpace(teamA)), strings.ToLower(strings.TrimSpace(teamB))
	for _, m := range matches {
		if strings.ToLower(m.TeamA) == a && strings.ToLower(m.TeamB) == b {
			return m
		}
	}
	log.Fatalf("no match %q v %q in the active tournament", teamA, teamB)
	return models.Match{}
}

func userNames(users []models.User) string {
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.DisplayName
	}
	return strings.Join(names, ", ")
}

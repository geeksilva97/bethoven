package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"bethoven/internal/config"
	"bethoven/internal/results"
	"bethoven/internal/results/espn"
)

// runCheckFeed is a diagnostic: it hits the real results feed once and prints
// what we'd get, WITHOUT touching the database. Use it to confirm the ESPN
// league slug is right and the regulation-90' score is derivable for finished
// matches before the first poll (no API key needed — the feed is keyless).
//
// Usage: bethoven check-feed [LEAGUE]
//
// League defaults to BETHOVEN_RESULTS_LEAGUE (or "fifa.world") and can be
// overridden by the argument.
func runCheckFeed(args []string) {
	cfg := config.Load()
	league := cfg.Results.League
	if len(args) > 0 && args[0] != "" {
		league = args[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client := espn.New(league, "")
	feed, err := client.Fetch(ctx)
	if err != nil {
		log.Fatalf("fetch %q failed: %v", league, err)
	}

	var finished, withReg90 int
	var noReg90 []results.FeedMatch
	teams := map[string]struct{}{}
	for _, m := range feed {
		teams[m.HomeTeam] = struct{}{}
		teams[m.AwayTeam] = struct{}{}
		if !m.Finished {
			continue
		}
		finished++
		if m.Reg90 != nil {
			withReg90++
		} else {
			noReg90 = append(noReg90, m) // finished but 90' score not derivable -> admin enters by hand
		}
	}

	fmt.Printf("league %q: %d matches, %d finished\n", league, len(feed), finished)
	fmt.Printf("  regulation-90' score derivable: %d/%d finished\n", withReg90, finished)
	if len(noReg90) > 0 {
		fmt.Printf("  %d finished match(es) WITHOUT a derivable 90' score (would be left for the admin):\n", len(noReg90))
		for _, m := range noReg90 {
			fmt.Printf("    - %s v %s [%s]\n", m.HomeTeam, m.AwayTeam, m.ExternalRef)
		}
	}

	// The feed's exact team spellings — compare against fixtures.json to
	// pre-populate teamAliases (in internal/service/service_sync.go) for any that
	// differ, so reconciliation matches on the first poll.
	fmt.Printf("\nteam names as the feed spells them (%d):\n", len(teams))
	names := make([]string, 0, len(teams))
	for n := range teams {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("  %s\n", n)
	}
}

package service

import (
	"testing"
	"time"

	"bethoven/internal/ai"
	"bethoven/internal/models"
)

// partByName finds a player's participation entry in the digest, or nil if absent
// (a fully-participating player is intentionally omitted).
func partByName(parts []ai.PlayerParticipation, name string) *ai.PlayerParticipation {
	for i := range parts {
		if parts[i].Name == name {
			return &parts[i]
		}
	}
	return nil
}

// TestDefectorFlag covers the server-computed defector signal: it fires only for a
// player who PLAYED and then abandoned the pool down the stretch once the tournament
// has reached its business end — and never for a late-joiner-only case, a never-start,
// or a mid-run flaky player who came back.
func TestDefectorFlag(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	deserter, _ := svc.Register("SHA256:deserter", testInvite, "Deserter")
	loyal, _ := svc.Register("SHA256:loyal", testInvite, "Loyal")
	svc.Register("SHA256:ghost", testInvite, "Ghost") // never bets
	flaky, _ := svc.Register("SHA256:flaky", testInvite, "Flaky")

	// Four finished group matches, all open to the early registrants.
	m1 := addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))
	m2 := addMatch(t, store, svc.tournamentID, base.Add(4*time.Hour))
	m3 := addMatch(t, store, svc.tournamentID, base.Add(6*time.Hour))
	m4 := addMatch(t, store, svc.tournamentID, base.Add(8*time.Hour))

	// Deserter played m1 then walked away (RecentSkips = 3).
	betOK(t, svc, deserter.ID, m1, 1, 0)
	// Loyal bet everything — excluded from the digest entirely.
	betOK(t, svc, loyal.ID, m1, 1, 0)
	betOK(t, svc, loyal.ID, m2, 1, 0)
	betOK(t, svc, loyal.ID, m3, 1, 0)
	betOK(t, svc, loyal.ID, m4, 1, 0)
	// Flaky skipped the middle but came back for the last one (RecentSkips = 0).
	betOK(t, svc, flaky.ID, m1, 1, 0)
	betOK(t, svc, flaky.ID, m4, 1, 0)
	// Ghost never bet at all.

	for _, m := range []int64{m1, m2, m3, m4} {
		if err := svc.EnterResult(admin, m, 1, 0); err != nil {
			t.Fatalf("EnterResult m=%d: %v", m, err)
		}
	}

	// A knockout match has kicked off ⇒ the tournament is in its business end.
	addTeamMatch(t, store, svc.tournamentID, "X", "Y", models.PhaseRound16, base.Add(10*time.Hour))
	fc.T = base.Add(11 * time.Hour)
	if !svc.tournamentLate() {
		t.Fatal("tournamentLate should be true once a knockout has kicked off")
	}

	parts := svc.participationDigest()

	if d := partByName(parts, "Deserter"); d == nil || !d.Defector {
		t.Errorf("Deserter should be flagged a defector: %+v", d)
	}
	if partByName(parts, "Loyal") != nil {
		t.Error("Loyal bet everything and must be omitted from the digest")
	}
	if g := partByName(parts, "Ghost"); g == nil || g.Defector || !g.NeverPicked {
		t.Errorf("Ghost never started — never a defector: %+v", g)
	}
	if f := partByName(parts, "Flaky"); f == nil || f.Defector {
		t.Errorf("Flaky came back for the last game — not a defector: %+v", f)
	}
}

// TestDefectorFlagRequiresLateJoinerExemptAndEarlyGate: a late joiner who bet all of
// their available games is not a defector, and a trailing tail is NOT branded a
// desertion before the tournament reaches its business end.
func TestDefectorFlagLateJoinerAndEarlyGate(t *testing.T) {
	svc, store, fc := newTestService(t)
	admin, _ := svc.Register(adminFP, "", "Boss")
	early, _ := svc.Register("SHA256:early", testInvite, "Early")

	// A schedule that is still early: 4 finished + 3 unfinished group games (57% done),
	// no knockout kicked off, so tournamentLate is false.
	m1 := addMatch(t, store, svc.tournamentID, base.Add(2*time.Hour))
	m2 := addMatch(t, store, svc.tournamentID, base.Add(4*time.Hour))
	m3 := addMatch(t, store, svc.tournamentID, base.Add(6*time.Hour))
	m4 := addMatch(t, store, svc.tournamentID, base.Add(8*time.Hour))
	addMatch(t, store, svc.tournamentID, base.Add(30*time.Hour))
	addMatch(t, store, svc.tournamentID, base.Add(32*time.Hour))
	addMatch(t, store, svc.tournamentID, base.Add(34*time.Hour))

	// Early played m1 then went quiet for m2-m4 (RecentSkips = 3) — but it's too early.
	betOK(t, svc, early.ID, m1, 1, 0)

	// A late joiner who registers after m1/m2 kicked off, then bets all THEIR games.
	fc.T = base.Add(5 * time.Hour)
	latecomer, _ := svc.Register("SHA256:late", testInvite, "Latecomer")
	betOK(t, svc, latecomer.ID, m3, 1, 0)
	betOK(t, svc, latecomer.ID, m4, 1, 0)

	// Now settle the four early games (EnterResult is clock-independent).
	for _, m := range []int64{m1, m2, m3, m4} {
		if err := svc.EnterResult(admin, m, 1, 0); err != nil {
			t.Fatalf("EnterResult m=%d: %v", m, err)
		}
	}

	if svc.tournamentLate() {
		t.Fatal("tournamentLate should be false: no knockout kicked off and <70% finished")
	}

	parts := svc.participationDigest()
	if e := partByName(parts, "Early"); e == nil || e.Defector {
		t.Errorf("Early's tail predates the business end — not a defector yet: %+v", e)
	}
	if l := partByName(parts, "Latecomer"); l == nil || l.Defector || !l.JoinedLate {
		t.Errorf("Latecomer bet all their available games — a late joiner, not a defector: %+v", l)
	}
}

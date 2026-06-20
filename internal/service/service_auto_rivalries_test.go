package service

import (
	"testing"

	"bethoven/internal/ai"
)

// setupRivalryService registers an admin plus three players and returns the admin.
func setupRivalryService(t *testing.T) (*Service, map[string]int64) {
	t.Helper()
	svc, _, _ := newTestService(t)
	admin, err := svc.Register(adminFP, "", "Boss")
	if err != nil {
		t.Fatalf("register admin: %v", err)
	}
	ids := map[string]int64{"Boss": admin.ID}
	for fp, name := range map[string]string{"SHA256:a": "Ana", "SHA256:b": "Bob", "SHA256:c": "Cara"} {
		u, err := svc.Register(fp, testInvite, name)
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		ids[name] = u.ID
	}
	return svc, ids
}

func TestSetAutoRivalriesResolvesAndDedupes(t *testing.T) {
	svc, _ := setupRivalryService(t)

	// Admin curates Ana vs Bob — auto must never override it.
	admin, _ := svc.Lookup(adminFP)
	if err := svc.AddRivalry(admin, mustID(t, svc, "Ana"), mustID(t, svc, "Bob"), "admin feud"); err != nil {
		t.Fatalf("AddRivalry: %v", err)
	}

	err := svc.SetAutoRivalries([]ai.Rivalry{
		{A: "Ana", B: "Bob", Note: "should be dropped (admin owns this pair)"},
		{A: "Ana", B: "Cara", Note: "neck and neck"},
		{A: "Ana", B: "Ghost", Note: "unknown player dropped"},
		{A: "bob", B: "cara", Note: "case-insensitive resolve"},
	})
	if err != nil {
		t.Fatalf("SetAutoRivalries: %v", err)
	}

	view, err := svc.AutoRivalriesView(admin)
	if err != nil {
		t.Fatalf("AutoRivalriesView: %v", err)
	}
	if len(view) != 2 {
		t.Fatalf("want 2 auto rivalries (admin pair + ghost dropped), got %d: %+v", len(view), view)
	}

	// CommentConfig must show 3 total: the admin pair once, plus the two auto pairs.
	cfg := svc.CommentConfig()
	if len(cfg.Rivalries) != 3 {
		t.Fatalf("CommentConfig rivalries = %d, want 3: %+v", len(cfg.Rivalries), cfg.Rivalries)
	}
}

func TestPinnedAutoRivalrySurvivesRegen(t *testing.T) {
	svc, _ := setupRivalryService(t)
	admin, _ := svc.Lookup(adminFP)

	if err := svc.SetAutoRivalries([]ai.Rivalry{{A: "Ana", B: "Bob", Note: "early lead battle"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.PinAutoRivalry(admin, 0, true); err != nil {
		t.Fatalf("pin: %v", err)
	}

	// A later detection pass returns a totally different set; the pinned one must stay.
	if err := svc.SetAutoRivalries([]ai.Rivalry{{A: "Ana", B: "Cara", Note: "new battle"}}); err != nil {
		t.Fatalf("regen: %v", err)
	}

	view, _ := svc.AutoRivalriesView(admin)
	if len(view) != 2 {
		t.Fatalf("pinned + new = 2, got %d: %+v", len(view), view)
	}
	var foundPinned bool
	for _, r := range view {
		if r.Pinned && r.Note == "early lead battle" {
			foundPinned = true
		}
	}
	if !foundPinned {
		t.Fatalf("pinned rivalry did not survive regen: %+v", view)
	}
}

func TestEditAutoRivalryPins(t *testing.T) {
	svc, _ := setupRivalryService(t)
	admin, _ := svc.Lookup(adminFP)

	_ = svc.SetAutoRivalries([]ai.Rivalry{{A: "Ana", B: "Bob", Note: "orig"}})
	if err := svc.EditAutoRivalry(admin, 0, "edited by admin"); err != nil {
		t.Fatalf("EditAutoRivalry: %v", err)
	}
	view, _ := svc.AutoRivalriesView(admin)
	if len(view) != 1 || view[0].Note != "edited by admin" || !view[0].Pinned {
		t.Fatalf("edit should change note and pin: %+v", view)
	}
}

func TestAutoRivalriesRequireAdmin(t *testing.T) {
	svc, _ := setupRivalryService(t)
	player, _ := svc.Lookup("SHA256:a")
	if _, err := svc.AutoRivalriesView(player); err == nil {
		t.Fatal("AutoRivalriesView must reject non-admin")
	}
	if err := svc.ClearAutoRivalries(player); err == nil {
		t.Fatal("ClearAutoRivalries must reject non-admin")
	}
}

func mustID(t *testing.T, svc *Service, name string) int64 {
	t.Helper()
	id, ok := svc.nameToID()[lower(name)]
	if !ok {
		t.Fatalf("no user named %q", name)
	}
	return id
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

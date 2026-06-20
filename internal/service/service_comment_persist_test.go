package service

import (
	"testing"

	"bethoven/internal/ai"
)

func TestSaveAndLoadComments(t *testing.T) {
	svc, _, _ := newTestService(t)
	ana, _ := svc.Register("SHA256:a", testInvite, "Ana")
	bob, _ := svc.Register("SHA256:b", testInvite, "Bob")

	set := []ai.Comment{
		{UserID: ana.ID, Player: "Ana", Text: "leading the pack", At: svc.Now()},
		{UserID: bob.ID, Player: "Bob", Text: "chasing hard", At: svc.Now()},
	}
	if err := svc.SaveComments(set); err != nil {
		t.Fatalf("SaveComments: %v", err)
	}

	got := svc.LoadComments()
	if len(got) != 2 {
		t.Fatalf("LoadComments returned %d, want 2", len(got))
	}
	byID := map[int64]string{}
	for _, c := range got {
		byID[c.UserID] = c.Text
	}
	if byID[ana.ID] != "leading the pack" || byID[bob.ID] != "chasing hard" {
		t.Fatalf("round-trip mismatch: %+v", byID)
	}

	// A single upsert changes only that player's row.
	if err := svc.SaveComment(ai.Comment{UserID: ana.ID, Player: "Ana", Text: "now slipping", At: svc.Now()}); err != nil {
		t.Fatalf("SaveComment: %v", err)
	}
	got = svc.LoadComments()
	byID = map[int64]string{}
	for _, c := range got {
		byID[c.UserID] = c.Text
	}
	if byID[ana.ID] != "now slipping" || byID[bob.ID] != "chasing hard" {
		t.Fatalf("upsert should change only Ana: %+v", byID)
	}
}

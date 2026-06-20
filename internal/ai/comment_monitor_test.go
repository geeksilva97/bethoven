package ai

import (
	"testing"
	"time"
)

func TestCommentMonitorSeed(t *testing.T) {
	mo := NewCommentMonitor("test-model", time.Hour)
	t0 := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)

	mo.Seed([]CommentAction{
		{At: t0, Player: "Ana", Text: "leading", Outcome: "written"},
		{At: t0.Add(time.Minute), Player: "Bob", Text: "chasing", Outcome: "written"},
	}, t0.Add(time.Minute))

	st := mo.Status()
	if st.Written != 2 {
		t.Fatalf("Written = %d, want 2", st.Written)
	}
	if !st.LastRun.Equal(t0.Add(time.Minute)) {
		t.Fatalf("LastRun = %v, want %v", st.LastRun, t0.Add(time.Minute))
	}
	// Activity is newest-first.
	act := mo.Activity(0)
	if len(act) != 2 || act[0].Player != "Bob" || act[1].Player != "Ana" {
		t.Fatalf("Activity newest-first mismatch: %+v", act)
	}

	// A subsequent real write keeps accumulating on top of the seed.
	mo.markRun(t0.Add(2 * time.Hour))
	mo.record(CommentAction{At: t0.Add(2 * time.Hour), Player: "Cara", Text: "new", Outcome: "written"})
	if st := mo.Status(); st.Written != 3 {
		t.Fatalf("after a real write Written = %d, want 3", st.Written)
	}
}

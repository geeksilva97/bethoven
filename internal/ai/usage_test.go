package ai

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestUsageAdd(t *testing.T) {
	var u Usage
	u.add(anthropic.Usage{InputTokens: 100, OutputTokens: 20, ServerToolUse: anthropic.ServerToolUsage{WebSearchRequests: 2}})
	u.add(anthropic.Usage{InputTokens: 50, OutputTokens: 10, ServerToolUse: anthropic.ServerToolUsage{WebSearchRequests: 1}})
	if u.Calls != 2 {
		t.Errorf("Calls = %d, want 2", u.Calls)
	}
	if u.InputTokens != 150 {
		t.Errorf("InputTokens = %d, want 150", u.InputTokens)
	}
	if u.OutputTokens != 30 {
		t.Errorf("OutputTokens = %d, want 30", u.OutputTokens)
	}
	if u.WebSearches != 3 {
		t.Errorf("WebSearches = %d, want 3", u.WebSearches)
	}
}

func TestAggregateUsage(t *testing.T) {
	// Two bet records (one with web search), two comment records (a single pass'
	// two calls), one live record. claude-sonnet-4-6 is priced; the bogus model is not.
	log := strings.Join([]string{
		`{"at":"2026-06-19T10:00:00Z","category":"bet","model":"claude-sonnet-4-6","calls":3,"input_tokens":1000000,"output_tokens":200000,"web_searches":5,"latency_ms":4000,"ok":true}`,
		`{"at":"2026-06-19T11:00:00Z","category":"bet","model":"claude-sonnet-4-6","calls":1,"input_tokens":500000,"output_tokens":100000,"latency_ms":2000,"ok":true}`,
		`{"at":"2026-06-19T12:00:00Z","category":"comment","model":"claude-sonnet-4-6","calls":1,"input_tokens":200000,"output_tokens":50000,"latency_ms":3000,"ok":true}`,
		`{"at":"2026-06-19T12:00:05Z","category":"comment","model":"claude-sonnet-4-6","calls":1,"input_tokens":300000,"output_tokens":80000,"latency_ms":5000,"ok":true}`,
		`{"at":"2026-06-19T13:00:00Z","category":"live","model":"claude-sonnet-4-6","calls":1,"input_tokens":100000,"output_tokens":10000,"latency_ms":1000,"ok":true}`,
		``,                 // trailing blank line tolerated
		`{not valid json}`, // malformed line skipped
	}, "\n")

	rep := aggregateUsage(strings.NewReader(log))

	if len(rep.Categories) != 3 {
		t.Fatalf("got %d categories, want 3", len(rep.Categories))
	}
	// Order must be bet, comment, live.
	if rep.Categories[0].Category != "bet" || rep.Categories[1].Category != "comment" || rep.Categories[2].Category != "live" {
		t.Fatalf("category order = %v", []string{rep.Categories[0].Category, rep.Categories[1].Category, rep.Categories[2].Category})
	}

	bet := rep.Categories[0]
	if bet.Calls != 4 {
		t.Errorf("bet calls = %d, want 4", bet.Calls)
	}
	if bet.InputTokens != 1_500_000 || bet.OutputTokens != 300_000 {
		t.Errorf("bet tokens = %d/%d, want 1500000/300000", bet.InputTokens, bet.OutputTokens)
	}
	if bet.WebSearches != 5 {
		t.Errorf("bet web searches = %d, want 5", bet.WebSearches)
	}
	// Avg latency is per-operation (2 lines): (4000+2000)/2 = 3000.
	if bet.AvgLatencyMS != 3000 {
		t.Errorf("bet avg latency = %d, want 3000", bet.AvgLatencyMS)
	}
	// Cost: sonnet = $3/Mtok in, $15/Mtok out → 1.5*3 + 0.3*15 = 4.5 + 4.5 = 9.0;
	// plus web search 5/1000*10 = 0.05 → 9.05.
	if got := bet.EstCostUSD; got < 9.04 || got > 9.06 {
		t.Errorf("bet cost = %.4f, want ~9.05", got)
	}
	if want := time.Date(2026, 6, 19, 11, 0, 0, 0, time.UTC); !bet.LastAt.Equal(want) {
		t.Errorf("bet LastAt = %v, want %v", bet.LastAt, want)
	}

	// Comment pass aggregates its two calls under one category.
	comment := rep.Categories[1]
	if comment.Calls != 2 || comment.InputTokens != 500_000 || comment.OutputTokens != 130_000 {
		t.Errorf("comment = calls %d in %d out %d", comment.Calls, comment.InputTokens, comment.OutputTokens)
	}

	if rep.Total.Calls != 7 {
		t.Errorf("total calls = %d, want 7", rep.Total.Calls)
	}
	if len(rep.UnknownModels) != 0 {
		t.Errorf("unexpected unknown models: %v", rep.UnknownModels)
	}
}

func TestAggregateUsageUnknownModel(t *testing.T) {
	log := `{"at":"2026-06-19T10:00:00Z","category":"bet","model":"gpt-9","calls":1,"input_tokens":1000000,"output_tokens":1000000,"latency_ms":1000,"ok":true}`
	rep := aggregateUsage(strings.NewReader(log))
	if len(rep.UnknownModels) != 1 || rep.UnknownModels[0] != "gpt-9" {
		t.Fatalf("UnknownModels = %v, want [gpt-9]", rep.UnknownModels)
	}
	// Unknown model contributes zero token cost (no web search here ⇒ $0).
	if rep.Total.EstCostUSD != 0 {
		t.Errorf("cost = %.4f, want 0 for unknown model", rep.Total.EstCostUSD)
	}
	// But tokens are still counted.
	if rep.Total.InputTokens != 1_000_000 {
		t.Errorf("input tokens = %d, want 1000000", rep.Total.InputTokens)
	}
}

func TestUsageLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai_usage.log")
	l := NewUsageLog(path)

	l.Record("bet", "claude-sonnet-4-6", Usage{Calls: 1, InputTokens: 1_000_000, OutputTokens: 200_000}, 2*time.Second, true)
	l.Record("comment", "claude-sonnet-4-6", Usage{Calls: 2, InputTokens: 500_000, OutputTokens: 100_000}, 3*time.Second, true)

	rep, err := l.Report()
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(rep.Categories) != 2 {
		t.Fatalf("got %d categories, want 2", len(rep.Categories))
	}
	if rep.Total.Calls != 3 {
		t.Errorf("total calls = %d, want 3", rep.Total.Calls)
	}
	if rep.Total.InputTokens != 1_500_000 || rep.Total.OutputTokens != 300_000 {
		t.Errorf("total tokens = %d/%d", rep.Total.InputTokens, rep.Total.OutputTokens)
	}
}

func TestUsageLogNilAndEmptyAreNoOps(t *testing.T) {
	// nil receiver must not panic.
	var nilLog *UsageLog
	nilLog.Record("bet", "m", Usage{Calls: 1}, time.Second, true)
	if rep, err := nilLog.Report(); err != nil || rep.Total.Calls != 0 {
		t.Errorf("nil Report = %+v, %v", rep, err)
	}

	// Empty path: Record is a no-op, Report is empty (no file created).
	l := NewUsageLog("")
	l.Record("bet", "m", Usage{Calls: 1}, time.Second, true)
	if rep, err := l.Report(); err != nil || rep.Total.Calls != 0 {
		t.Errorf("empty-path Report = %+v, %v", rep, err)
	}
}

func TestReportMissingFile(t *testing.T) {
	l := NewUsageLog(filepath.Join(t.TempDir(), "does-not-exist.log"))
	rep, err := l.Report()
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if rep.Total.Calls != 0 {
		t.Errorf("missing file should give empty report, got %d calls", rep.Total.Calls)
	}
}

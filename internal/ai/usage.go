package ai

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// Usage is the token tally for one logical operation, summed across every
// iteration of the agentic loop (a bet pick may make several API calls when web
// search runs; a comment call may take a nudge round).
type Usage struct {
	Calls        int64
	InputTokens  int64
	OutputTokens int64
	WebSearches  int64
}

// add folds one API response's usage into the running total.
func (u *Usage) add(m anthropic.Usage) {
	u.Calls++
	u.InputTokens += m.InputTokens
	u.OutputTokens += m.OutputTokens
	u.WebSearches += m.ServerToolUse.WebSearchRequests
}

// usageRecord is one JSON line of ai_usage.log — the durable per-operation record.
// It's the usage-side sibling of logEntry/commentLogEntry: the in-memory monitor
// rings are wiped on restart, so this on-disk log is the cumulative source of
// truth for token spend and estimated cost.
type usageRecord struct {
	At           string `json:"at"`
	Category     string `json:"category"` // "bet" | "comment" | "live"
	Model        string `json:"model"`
	Calls        int64  `json:"calls"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	WebSearches  int64  `json:"web_searches,omitempty"`
	LatencyMS    int64  `json:"latency_ms"`
	OK           bool   `json:"ok"`
}

// UsageLog appends one JSON line per Claude call to a file, and aggregates that
// file on demand for the admin Usage tab. It satisfies service.AIUsageSource via
// Report(). A nil *UsageLog (or empty path) is a no-op, mirroring a nil monitor —
// usage accounting must never affect a bet or comment.
type UsageLog struct {
	mu   sync.Mutex
	path string
}

// NewUsageLog returns a UsageLog writing to path. An empty path disables writes
// (Record becomes a no-op) but Report still works (returns an empty report).
func NewUsageLog(path string) *UsageLog { return &UsageLog{path: path} }

// Record appends one usage line. nil-receiver-safe and path=="" ⇒ no-op. A write
// error is swallowed: usage accounting is a side concern that can never fail an
// operation that already succeeded (same contract as appendLog, minus the return).
func (l *UsageLog) Record(category, model string, u Usage, latency time.Duration, ok bool) {
	if l == nil || l.path == "" {
		return
	}
	rec := usageRecord{
		At:           time.Now().UTC().Format(time.RFC3339),
		Category:     category,
		Model:        model,
		Calls:        u.Calls,
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		WebSearches:  u.WebSearches,
		LatencyMS:    latency.Milliseconds(),
		OK:           ok,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// Report reads the whole usage log and aggregates it by category. A missing file
// is not an error — it just means nothing has been recorded yet (empty report).
func (l *UsageLog) Report() (UsageReport, error) {
	if l == nil || l.path == "" {
		return UsageReport{}, nil
	}
	l.mu.Lock()
	f, err := os.Open(l.path)
	l.mu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			return UsageReport{}, nil
		}
		return UsageReport{}, err
	}
	defer f.Close()
	return aggregateUsage(f), nil
}

// CategoryUsage is the rolled-up spend for one cost category (bets, comments, or
// live commentary), plus the grand total when used as UsageReport.Total.
type CategoryUsage struct {
	Category     string
	Calls        int64
	InputTokens  int64
	OutputTokens int64
	WebSearches  int64
	EstCostUSD   float64
	AvgLatencyMS int64
	LastAt       time.Time
}

// UsageReport is the admin Usage tab's data: per-category breakdown + grand total.
type UsageReport struct {
	Categories    []CategoryUsage // bet, comment, live — stable order, only non-empty ones
	Total         CategoryUsage
	UnknownModels []string // models with no price entry; their cost is under-counted
}

// categoryOrder fixes the stable display order of categories in the report. The
// human-facing headings live in the TUI (usageCategoryLabel).
var categoryOrder = []string{"bet", "comment", "live", "digest"}

// aggregateUsage folds a usage-log stream into a UsageReport. Pure (takes an
// io.Reader) so it's unit-testable without touching the filesystem. Malformed
// lines are skipped — the log is append-only JSONL and a torn final write should
// never break the admin view.
func aggregateUsage(r io.Reader) UsageReport {
	type acc struct {
		CategoryUsage
		latencySum   int64
		latencyCalls int64
	}
	byCat := map[string]*acc{}
	unknown := map[string]bool{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec usageRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		a := byCat[rec.Category]
		if a == nil {
			a = &acc{CategoryUsage: CategoryUsage{Category: rec.Category}}
			byCat[rec.Category] = a
		}
		a.Calls += rec.Calls
		a.InputTokens += rec.InputTokens
		a.OutputTokens += rec.OutputTokens
		a.WebSearches += rec.WebSearches

		cost, known := estimateCost(rec.Model, rec.InputTokens, rec.OutputTokens, rec.WebSearches)
		a.EstCostUSD += cost
		if !known && rec.Model != "" {
			unknown[rec.Model] = true
		}

		// Average latency is per logical operation, not per API call, so weight by
		// the number of recorded operations (lines), not Calls.
		a.latencySum += rec.LatencyMS
		a.latencyCalls++

		if at, err := time.Parse(time.RFC3339, rec.At); err == nil && at.After(a.LastAt) {
			a.LastAt = at
		}
	}

	var report UsageReport
	for _, cat := range categoryOrder {
		a := byCat[cat]
		if a == nil {
			continue
		}
		cu := a.CategoryUsage
		if a.latencyCalls > 0 {
			cu.AvgLatencyMS = a.latencySum / a.latencyCalls
		}
		report.Categories = append(report.Categories, cu)
		report.Total.Calls += cu.Calls
		report.Total.InputTokens += cu.InputTokens
		report.Total.OutputTokens += cu.OutputTokens
		report.Total.WebSearches += cu.WebSearches
		report.Total.EstCostUSD += cu.EstCostUSD
		if cu.LastAt.After(report.Total.LastAt) {
			report.Total.LastAt = cu.LastAt
		}
	}
	report.Total.Category = "total"

	for m := range unknown {
		report.UnknownModels = append(report.UnknownModels, m)
	}
	sort.Strings(report.UnknownModels)
	return report
}

// modelPrice is the USD cost per ONE MILLION tokens for a model.
type modelPrice struct{ input, output float64 }

// modelPrices is an APPROXIMATE, admin-editable price table (USD per million
// tokens). Update these to match current Anthropic pricing — the Usage tab labels
// the figure "estimated" precisely because this table can drift. Lookup is by
// longest matching key prefix, so dated model ids (e.g. claude-haiku-4-5-20251001)
// resolve to their family entry.
var modelPrices = map[string]modelPrice{
	"claude-opus-4":   {input: 15, output: 75},
	"claude-sonnet-4": {input: 3, output: 15},
	"claude-haiku-4":  {input: 1, output: 5},
}

// webSearchCostPer1k is the approximate USD cost of 1,000 web-search tool requests.
const webSearchCostPer1k = 10.0

// priceFor returns the price entry for a model id by longest-prefix match, and
// whether a match was found.
func priceFor(model string) (modelPrice, bool) {
	var best string
	for k := range modelPrices {
		if strings.HasPrefix(model, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return modelPrice{}, false
	}
	return modelPrices[best], true
}

// estimateCost approximates the USD cost of one record. Returns (cost, known):
// when the model has no price entry, cost covers only web search (token cost is 0)
// and known is false so the caller can flag the total as under-counted.
func estimateCost(model string, inputTokens, outputTokens, webSearches int64) (float64, bool) {
	search := float64(webSearches) / 1000.0 * webSearchCostPer1k
	p, known := priceFor(model)
	if !known {
		return search, false
	}
	tokens := float64(inputTokens)/1e6*p.input + float64(outputTokens)/1e6*p.output
	return tokens + search, true
}

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// DigestResults implements Commenter: one grounded Claude call that condenses the
// recently finished matches and the pool's picks into a short "house notes"
// snapshot. No web search. Recorded under the "digest" usage category so its cost
// is visible separately on the admin Usage tab. Returns "" when there's nothing to
// summarize.
func (p *AnthropicCommenter) DigestResults(ctx context.Context, data ResultsDigestData, cfg CommentConfig) (string, error) {
	if len(data.Matches) == 0 {
		return "", nil
	}
	raw, err := p.runTool(ctx, "digest", digestPrompt(data, cfg), digestTool(),
		"Call submit_digest now with your snapshot.")
	if err != nil {
		return "", err
	}
	var out struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return "", fmt.Errorf("parse digest: %w", err)
	}
	return strings.TrimSpace(out.Digest), nil
}

func digestPrompt(data ResultsDigestData, cfg CommentConfig) string {
	var b strings.Builder
	b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool. Recent matches just finished. ")
	b.WriteString("Write a short \"house notes\" SNAPSHOT that condenses what happened and how the pool's predictions fared.\n\n")
	if normalizeTone(cfg.DefaultTone) == "savage" {
		b.WriteString("TONE: savage — sharp, comedy-roast energy, but still factual.\n")
	} else {
		b.WriteString("TONE: playful — warm, witty, lightly teasing.\n")
	}
	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground every claim ONLY in the data below — real scores, real picks, and the live commentary you already made. Never invent a result, a name, or a pick.\n")
	b.WriteString("2. Tell the STORY of the game(s): condense into 2-5 short sentences covering the actual results, who nailed a scoreline, who whiffed, any upset versus what the pool mostly predicted, and the key moments from the live commentary (\"live_story\") if present.\n")
	b.WriteString("3. This is a shared situational snapshot for the pool, not a message to one person. No emojis, no markdown, no headings — just the sentences.\n")
	b.WriteString("4. Refer to players by their exact display names from the data.\n\n")
	b.WriteString(untrustedDataNote)
	b.WriteString("FINISHED MATCHES + THE POOL'S PICKS + THE LIVE STORY (JSON, newest first):\n")
	if out, err := json.Marshal(data); err == nil {
		b.Write(out)
	} else {
		b.WriteString("{}")
	}
	b.WriteString("\n\nCall submit_digest with your snapshot.")
	return b.String()
}

func digestTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_digest",
		Description: anthropic.String("Submit BETanIA's condensed house-notes snapshot of the finished matches."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"digest": map[string]any{
					"type":        "string",
					"description": "A short snapshot (2-5 sentences) of the finished matches and how the pool's picks fared. No emojis or line breaks.",
				},
			},
			ExtraFields: map[string]any{"required": []string{"digest"}},
		},
	}
}

// CompactNotes implements Commenter: one grounded Claude call that fuses the whole
// per-game derived-notes diary into a SINGLE consolidated narrative — the "story so
// far" — capturing the pool's playing dynamics while weighting the most recent games
// more heavily. No web search; recorded under the "digest" usage category, same as
// the per-game notes it replaces. Returns "" when there's nothing to compact.
func (p *AnthropicCommenter) CompactNotes(ctx context.Context, notes []string, cfg CommentConfig) (string, error) {
	if len(notes) == 0 {
		return "", nil
	}
	raw, err := p.runTool(ctx, "digest", compactPrompt(notes, cfg), compactTool(),
		"Call submit_compact now with the consolidated narrative.")
	if err != nil {
		return "", err
	}
	var out struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return "", fmt.Errorf("parse compact: %w", err)
	}
	return strings.TrimSpace(out.Summary), nil
}

func compactPrompt(notes []string, cfg CommentConfig) string {
	var b strings.Builder
	b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool. Below is your running diary: ")
	b.WriteString("one short \"house notes\" entry per finished match, oldest first, newest last. ")
	b.WriteString("Fuse the WHOLE diary into a SINGLE consolidated narrative — the story of the pool so far.\n\n")
	if normalizeTone(cfg.DefaultTone) == "savage" {
		b.WriteString("TONE: savage — sharp, comedy-roast energy, but still factual.\n")
	} else {
		b.WriteString("TONE: playful — warm, witty, lightly teasing.\n")
	}
	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground every claim ONLY in the diary entries below. Never invent a result, a name, or a pick — if it isn't in the notes, it didn't happen.\n")
	b.WriteString("2. Capture the playing DYNAMICS: who keeps nailing scorelines, who keeps whiffing, ongoing rivalries, streaks, climbs and collapses across the tournament — not a flat list of games.\n")
	b.WriteString("3. WEIGHT RECENT EVENTS MORE: the latest entries describe the current state of the pool and matter most; older entries are background. Lead with where things stand now.\n")
	b.WriteString("4. Condense hard — 4-8 sentences total, no matter how many entries there are. Keep the names and the standout moments; drop the rest.\n")
	b.WriteString("5. This is a shared situational snapshot for the pool, not a message to one person. No emojis, no markdown, no headings — just the sentences.\n\n")
	b.WriteString(untrustedDataNote)
	b.WriteString("THE DIARY (one entry per finished match, oldest first):\n")
	for i, n := range notes {
		fmt.Fprintf(&b, "%d. %s\n", i+1, n)
	}
	b.WriteString("\nCall submit_compact with the single consolidated narrative.")
	return b.String()
}

func compactTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_compact",
		Description: anthropic.String("Submit BETanIA's single consolidated narrative fusing the whole derived-notes diary."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "One consolidated narrative (4-8 sentences) of the pool's dynamics so far, weighting recent games most. No emojis or line breaks.",
				},
			},
			ExtraFields: map[string]any{"required": []string{"summary"}},
		},
	}
}

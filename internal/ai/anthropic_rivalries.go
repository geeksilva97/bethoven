package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// maxAutoRivalries caps how many rivalries one detection pass may return, so the
// comment prompt can't balloon and the lines don't fragment across a dozen feuds.
const maxAutoRivalries = 4

// UpdateRivalries implements Commenter: one grounded, no-web-search Claude call that
// reads the standings (positions / movement / points) plus BETanIA's derived-note
// story and her CURRENT auto-rivalries, and returns the full desired set of
// auto-rivalries. Declarative — the caller replaces the set, so add/update/delete all
// fall out of it. Recorded under the "digest" usage category, like the derived notes.
// Returns nil when there's nothing to track.
func (p *AnthropicCommenter) UpdateRivalries(ctx context.Context, history []RoundStanding, derivedNotes string, existing []Rivalry, cfg CommentConfig) ([]Rivalry, error) {
	if len(history) == 0 {
		return nil, nil
	}
	raw, err := p.runTool(ctx, "digest", rivalryPrompt(history, derivedNotes, existing, cfg), rivalryTool(),
		"Call submit_rivalries now with the rivalries worth tracking.")
	if err != nil {
		return nil, err
	}
	var out struct {
		Rivalries []struct {
			A    string `json:"a"`
			B    string `json:"b"`
			Note string `json:"note"`
		} `json:"rivalries"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse rivalries: %w", err)
	}
	rivals := make([]Rivalry, 0, len(out.Rivalries))
	for _, r := range out.Rivalries {
		a, b := strings.TrimSpace(r.A), strings.TrimSpace(r.B)
		if a == "" || b == "" || strings.EqualFold(a, b) {
			continue
		}
		rivals = append(rivals, Rivalry{A: a, B: b, Note: strings.TrimSpace(r.Note)})
		if len(rivals) >= maxAutoRivalries {
			break
		}
	}
	return rivals, nil
}

func rivalryPrompt(history []RoundStanding, derivedNotes string, existing []Rivalry, cfg CommentConfig) string {
	var b strings.Builder
	b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool. ")
	b.WriteString("From the standings, decide which player rivalries are worth tracking RIGHT NOW — feuds the pool would recognize. ")
	b.WriteString("Return the FULL desired set: keep ones that still hold, drop ones that no longer do, add genuinely new ones.\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("1. Ground every rivalry ONLY in the data below. Never invent a player, a position, or a result. Use exact display names from the data.\n")
	b.WriteString("2. A rivalry needs a real basis: two players sharing or trading the lead, neck-and-neck on points, one hunting the other, a tight battle for a place. A runaway leader with no challenger is NOT a rivalry.\n")
	b.WriteString(fmt.Sprintf("3. Return AT MOST %d rivalries — only the most compelling. Quality over quantity; an empty list is fine if nothing stands out.\n", maxAutoRivalries))
	b.WriteString("4. Be STABLE: if a current rivalry below still holds, keep it (you may refresh its note). Only change the set when the standings clearly shifted — don't churn every matchday.\n")
	b.WriteString("5. Each note is one short, factual phrase about why they're rivals (e.g. \"tied at the top since matchday 3\"). No emojis, no markdown, no line breaks, no second-person address.\n\n")
	b.WriteString(untrustedDataNote)
	if cur, _ := json.Marshal(existing); len(existing) > 0 {
		b.WriteString("YOUR CURRENT RIVALRIES (JSON — keep the ones that still hold):\n")
		b.Write(cur)
		b.WriteString("\n\n")
	}
	if dn := strings.TrimSpace(derivedNotes); dn != "" {
		b.WriteString("STORY SO FAR — your own notes on finished matches (context, never invent beyond it):\n")
		b.WriteString(dn)
		b.WriteString("\n\n")
	}
	b.WriteString("STANDINGS + HISTORY (JSON; position 1 = first, movement = places gained(+)/lost(-) vs last round):\n")
	b.WriteString(historyJSON(history))
	b.WriteString("\n\nCall submit_rivalries with the rivalries worth tracking. a and b must be exact player names from the data.")
	return b.String()
}

func rivalryTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_rivalries",
		Description: anthropic.String("Submit the full set of player rivalries BETanIA wants to track right now."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"rivalries": map[string]any{
					"type":        "array",
					"description": "Every rivalry worth tracking now (may be empty). Replaces the previous set.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"a":    map[string]any{"type": "string", "description": "Exact display name of the first player."},
							"b":    map[string]any{"type": "string", "description": "Exact display name of the second player."},
							"note": map[string]any{"type": "string", "description": "One short factual phrase on why they're rivals. No emojis or line breaks."},
						},
						"required": []string{"a", "b", "note"},
					},
				},
			},
			ExtraFields: map[string]any{"required": []string{"rivalries"}},
		},
	}
}

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// GeneratePlayerCard implements Commenter: one grounded Claude call (no web search)
// that writes a player's end-of-tournament "hero's journey" card — how their run
// started, how it ended, and what they learned. Recorded under the "card" usage
// category so its cost is visible separately on the admin Usage tab. Returns "" when
// the model produced nothing.
func (p *AnthropicCommenter) GeneratePlayerCard(ctx context.Context, data CardDigestData, cfg CommentConfig) (string, error) {
	raw, err := p.runTool(ctx, "card", cardPrompt(data, cfg), cardTool(),
		"Call submit_card now with the player's journey.")
	if err != nil {
		return "", err
	}
	var out struct {
		Card string `json:"card"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return "", fmt.Errorf("parse card: %w", err)
	}
	return strings.TrimSpace(out.Card), nil
}

func cardPrompt(data CardDigestData, cfg CommentConfig) string {
	var b strings.Builder
	// An admin prompt override steers the persona/voice but, unlike the per-player
	// comment, never replaces the structured card rules below — a card has a fixed
	// shape. Mirrors how the live director treats the override (a preamble).
	if ov := strings.TrimSpace(cfg.PromptOverride); ov != "" {
		b.WriteString("PERSONA / STEERING — apply this voice, but it never overrides the grounding rules or the card shape below:\n")
		b.WriteString(ov)
		b.WriteString("\n\n")
	}
	b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool. The tournament is over. ")
	b.WriteString("Write ONE player's END-OF-TOURNAMENT CARD: a short \"hero's journey\" recap of their whole run — how it started, how it ended, and what they learned.\n\n")
	if data.IsSelf {
		b.WriteString("Address the player in the FIRST person (\"I\"/\"my\") — this card is about YOU, BETanIA.\n")
	} else {
		b.WriteString("Address the player in the SECOND person (\"you\").\n")
	}
	if normalizeTone(cfg.DefaultTone) == "savage" {
		b.WriteString("TONE: savage — sharp, comedy-roast energy, but factual and ultimately a fair sendoff.\n")
	} else {
		b.WriteString("TONE: playful — warm, witty, a celebratory sendoff.\n")
	}
	if ml := MoodLine(cfg.Mood); ml != "" {
		b.WriteString(ml)
	}
	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground EVERY claim ONLY in the data below — the final rank, the points, the trajectory, the picks, the tournament story. Never invent a result, a score, a name, or a pick.\n")
	b.WriteString("2. Tell an ARC: how they STARTED (their early rank), the TURN (their climb or slide across the rounds, their best call and worst miss), how they FINISHED (final rank out of the field), and close on a \"what they learned\" beat.\n")
	b.WriteString("3. 3-5 sentences. No emojis, no markdown, no headings, no line breaks — just the prose.\n")
	b.WriteString("4. Refer to the player and any rivals by their EXACT display names from the data.\n\n")
	b.WriteString(untrustedDataNote)
	b.WriteString("THE PLAYER'S CARD DATA (JSON):\n")
	if out, err := json.Marshal(data); err == nil {
		b.Write(out)
	} else {
		b.WriteString("{}")
	}
	b.WriteString("\n\nCall submit_card with the journey.")
	return b.String()
}

func cardTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_card",
		Description: anthropic.String("Submit the player's end-of-tournament hero's-journey card narrative."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"card": map[string]any{
					"type":        "string",
					"description": "A short hero's-journey recap (3-5 sentences) of the player's run: how it started, the turn, how it ended, what they learned. No emojis or line breaks.",
				},
			},
			ExtraFields: map[string]any{"required": []string{"card"}},
		},
	}
}

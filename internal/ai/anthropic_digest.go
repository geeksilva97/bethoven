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

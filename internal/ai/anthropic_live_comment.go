package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// WriteLiveComment implements LiveCommenter: a single grounded play-by-play line
// for the current live situation, in the active tone, aware of recent lines so it
// doesn't repeat itself. One model call, no web search. *AnthropicCommenter already
// holds the client/model, so it serves both the per-player and the live commentary.
func (p *AnthropicCommenter) WriteLiveComment(ctx context.Context, sit LiveSituation, recent []string, cfg CommentConfig) (string, error) {
	if len(sit.Matches) == 0 {
		return "", nil
	}
	raw, err := p.runTool(ctx, liveCommentPrompt(sit, recent, cfg), liveCommentTool(),
		"Call submit_live_comment now with your single line.")
	if err != nil {
		return "", err
	}
	var out struct {
		Comment string `json:"comment"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return "", fmt.Errorf("parse live comment: %w", err)
	}
	return strings.TrimSpace(out.Comment), nil
}

func liveCommentPrompt(sit LiveSituation, recent []string, cfg CommentConfig) string {
	var b strings.Builder

	// A full admin override replaces the persona/voice body; otherwise the built-in.
	if o := strings.TrimSpace(cfg.PromptOverride); o != "" {
		b.WriteString(o)
		b.WriteString("\n\n")
		b.WriteString("Now, in that voice, write ONE short LIVE play-by-play line about the current match situation below.\n")
	} else {
		b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool, doing LIVE commentary at the top of the leaderboard.\n\n")
		b.WriteString("Write ONE short line about what is happening RIGHT NOW: who is nailing the scoreline, who is climbing or sliding on the board, and which side the betting odds favour. Address the pool in general — not one single person.\n")
		if normalizeTone(cfg.DefaultTone) == "savage" {
			b.WriteString("TONE: savage — genuinely cutting and funny, comedy-roast energy.\n")
		} else {
			b.WriteString("TONE: playful — tease warmly and wittily, never mean.\n")
		}
	}

	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground every claim ONLY in the live data below. Never invent scores, names, odds, or events.\n")
	b.WriteString("2. One sentence, at most ~140 characters. No emojis and no line breaks.\n")
	b.WriteString("3. The score will change as the game moves — react to the CURRENT state.\n")
	b.WriteString("4. The \"odds\" field is American moneyline (negative = favourite, e.g. -180; positive = underdog). TRANSLATE it into plain favourite/underdog language (\"heavy favourites\", \"slight edge\", \"long shot\") — NEVER quote the raw number.\n")
	b.WriteString("5. Each match may include \"key_events\" (goals, cards) with the minute and a text description. You MAY name the scorer or call out a card using ONLY that text — never invent a scorer, minute, or event not listed there.\n")
	b.WriteString("6. A match may carry a \"phase\": \"halftime\" means it's the INTERVAL/break (not live play) — react to the break, not as if the ball is rolling; \"extra_time\"/\"penalties\" likewise. No phase ⇒ ordinary live play.\n")

	if len(recent) > 0 {
		b.WriteString("\nYOUR RECENT LINES (most recent last) — say something NEW, do not repeat these:\n")
		for _, r := range recent {
			fmt.Fprintf(&b, "- %s\n", r)
		}
	}

	b.WriteString("\n")
	b.WriteString(untrustedDataNote)
	b.WriteString("LIVE SITUATION (JSON):\n")
	b.WriteString(liveSituationJSON(sit))
	b.WriteString("\n\nCall submit_live_comment with your single line.")
	return b.String()
}

// liveSituationJSON marshals the situation for the model. Each pick's "pred" string
// (e.g. "2-1") is set by the service builder; PredA/PredB are json:"-" so the model
// sees only the combined string.
func liveSituationJSON(sit LiveSituation) string {
	out, err := json.Marshal(sit)
	if err != nil {
		return "{}"
	}
	return string(out)
}

func liveCommentTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_live_comment",
		Description: anthropic.String("Submit BETanIA's single live-commentary line."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"comment": map[string]any{
					"type":        "string",
					"description": "One short live line (<=140 chars, no emojis, no line breaks).",
				},
			},
			ExtraFields: map[string]any{"required": []string{"comment"}},
		},
	}
}

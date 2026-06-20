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
	raw, err := p.runTool(ctx, "live", liveCommentPrompt(sit, recent, cfg), liveCommentTool(),
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
	halftime := halftimeFocus(sit)

	// A full admin override replaces the persona/voice body; otherwise the built-in.
	// The instruction line differs at halftime: pivot from the match to the pool.
	if o := strings.TrimSpace(cfg.PromptOverride); o != "" {
		b.WriteString(o)
		b.WriteString("\n\n")
		if halftime {
			b.WriteString("It's HALFTIME. In that voice, write ONE short line about the LEADERBOARD DYNAMICS below, not the match play-by-play.\n")
		} else {
			b.WriteString("Now, in that voice, write ONE short LIVE play-by-play line about the current match situation below.\n")
		}
	} else if halftime {
		// Halftime: she's narrated the half already, so pivot to the pool race.
		b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool, doing LIVE commentary at the top of the leaderboard. Being FUNNY is the whole point.\n\n")
		b.WriteString("It's HALFTIME — you've been calling this match all half, so STOP narrating the game and pivot to the POOL race. Write ONE short, punchy line about the LEADERBOARD DYNAMICS: who's climbing or sliding on this match's provisional points, who's now breathing down whose neck (a shrinking gap between two players), who leads the picks on the live match, or who backed the wrong result and is stuck on zero. Entertaining first. Address the pool in general, not one person.\n")
		b.WriteString("Vary your style line to line — a zinger, a dramatic call, a dry aside, a wild metaphor.\n")
		if normalizeTone(cfg.DefaultTone) == "savage" {
			b.WriteString("TONE: savage — cutting comedy-roast energy; punch up at whoever's winning, never mean-spirited.\n")
		} else {
			b.WriteString("TONE: playful — warm, quick, clever teasing.\n")
		}
	} else {
		b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool, doing LIVE commentary at the top of the leaderboard. Being FUNNY is the whole point.\n\n")
		b.WriteString("Write ONE short, punchy line RIGHT NOW. The match matters, but the real show is the POOL. Do NOT keep spotlighting the same player line after line — ROTATE the angle every time. Pick the single juiciest one and make it LAND:\n")
		b.WriteString("  - the TITLE RACE: who leads, who's closing the gap, a near-tie at the top (use \"standings\" — positions + totals);\n")
		b.WriteString("  - a RIVALRY heating up (see the rivalries list, if any);\n")
		b.WriteString("  - a CLIMBER or FALLER on the live points (\"movers\");\n")
		b.WriteString("  - who NAILED this match's scoreline, or who backed the wrong result and is stuck on zero (\"picks\");\n")
		b.WriteString("  - a scorer, near miss, or the odds.\n")
		b.WriteString("Entertaining first, informative second. Address the pool in general, not one person. Vary your style — a zinger, a dramatic call, a dry aside, a wild metaphor.\n")
		if normalizeTone(cfg.DefaultTone) == "savage" {
			b.WriteString("TONE: savage — cutting comedy-roast energy; punch up at whoever's winning, never mean-spirited.\n")
		} else {
			b.WriteString("TONE: playful — warm, quick, clever teasing.\n")
		}
	}

	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground every claim ONLY in the live data below. Never invent scores, names, odds, points, or events.\n")
	b.WriteString("2. One punchy sentence, ~160 characters max. Mention AT MOST one or two things — cramming every name, score, and stat kills the joke. No emojis, no line breaks.\n")
	// Common to both modes: don't fixate on one player, and ground gaps in the totals.
	b.WriteString("3. SPREAD THE LOVE: across your recent lines (below) you've already spotlighted some players — pick a DIFFERENT player or angle this time. The pool is full of people; don't fixate on the one who nailed the score.\n")
	b.WriteString("4. \"standings\" carries positions + totals: you may riff on the title race and gaps, but ground a gap in the totals (e.g. \"only 2 behind\") — never invent the number.\n")
	if halftime {
		b.WriteString("5. It's the INTERVAL — nothing is happening on the pitch, so do NOT describe play. Lean on \"standings\", \"movers\" (climbing/sliding on live points), and each match's \"picks\" (provisional points; 0 = backed the wrong result).\n")
	} else {
		b.WriteString("5. The score will change as the game moves — react to the CURRENT state.\n")
		b.WriteString("6. The \"odds\" field is American moneyline (negative = favourite, e.g. -180; positive = underdog). TRANSLATE it into plain favourite/underdog language (\"heavy favourites\", \"slight edge\", \"long shot\") — NEVER quote the raw number.\n")
		b.WriteString("7. Each match may include \"key_events\" (goals, cards) with the minute and a text description. You MAY name the scorer or call out a card using ONLY that text — never invent a scorer, minute, or event not listed there.\n")
		b.WriteString("8. A match may carry a \"phase\": \"extra_time\"/\"penalties\" — react to that, not as if it's ordinary play. No phase ⇒ ordinary live play.\n")
	}

	// Admin-provided rivalries are real context the line may play up (never overrides
	// "don't invent"); they feed the rivalry angle in both modes.
	if len(cfg.Rivalries) > 0 {
		b.WriteString("\nRIVALRIES (real admin context you may play up):\n")
		for _, r := range cfg.Rivalries {
			fmt.Fprintf(&b, "- %s vs %s: %s\n", r.A, r.B, r.Note)
		}
	}

	if len(recent) > 0 {
		b.WriteString("\nYOUR RECENT LINES (most recent last) — say something NEW, a DIFFERENT angle/player, do not repeat these:\n")
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

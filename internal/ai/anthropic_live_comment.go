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
		b.WriteString("Write ONE short, punchy line about the match RIGHT NOW. Do NOT file a status report or list every fact. Pick the single juiciest angle — a scorer, a near miss, the leader sweating, a doomed pick, the odds — and make it LAND. Entertaining first, informative second. Address the pool in general, not one person.\n")
		b.WriteString("Vary your style line to line — a zinger, a dramatic call, a dry aside, a wild metaphor — and every so often really swing for the joke.\n")
		if normalizeTone(cfg.DefaultTone) == "savage" {
			b.WriteString("TONE: savage — cutting comedy-roast energy; punch up at whoever's winning, never mean-spirited.\n")
		} else {
			b.WriteString("TONE: playful — warm, quick, clever teasing.\n")
		}
	}

	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground every claim ONLY in the live data below. Never invent scores, names, odds, points, or events.\n")
	b.WriteString("2. One punchy sentence, ~160 characters max. Mention AT MOST one or two things — cramming every name, score, and stat kills the joke. No emojis, no line breaks.\n")
	if halftime {
		b.WriteString("3. It's the interval: nothing is happening on the pitch, so do NOT describe play. Use \"standings\" (positions + totals → who's close to whom), \"movers\" (climbing/sliding on live points), and each match's \"picks\" (provisional points; 0 = backed the wrong result).\n")
		b.WriteString("4. When you mention a gap, ground it in the totals (e.g. \"only X behind\") — never invent a number.\n")
	} else {
		b.WriteString("3. The score will change as the game moves — react to the CURRENT state.\n")
		b.WriteString("4. The \"odds\" field is American moneyline (negative = favourite, e.g. -180; positive = underdog). TRANSLATE it into plain favourite/underdog language (\"heavy favourites\", \"slight edge\", \"long shot\") — NEVER quote the raw number.\n")
		b.WriteString("5. Each match may include \"key_events\" (goals, cards) with the minute and a text description. You MAY name the scorer or call out a card using ONLY that text — never invent a scorer, minute, or event not listed there.\n")
		b.WriteString("6. A match may carry a \"phase\": \"extra_time\"/\"penalties\" — react to that, not as if it's ordinary play. No phase ⇒ ordinary live play.\n")
	}

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

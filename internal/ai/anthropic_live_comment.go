package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// WriteLiveComment implements LiveCommenter: a single grounded line for the
// current situation (in-play play-by-play, an about-to-start hype, or a just-
// finished result reaction), in the active tone and mood, aware of recent lines so
// it doesn't repeat itself — plus BETanIA's updated mood. One model call, no web
// search. The comment may be empty (stay silent). *AnthropicCommenter already holds
// the client/model, so it serves both the per-player and the director paths.
func (p *AnthropicCommenter) WriteLiveComment(ctx context.Context, sit LiveSituation, recent []string, cfg CommentConfig) (LiveOutput, error) {
	if !sit.hasContent() {
		return LiveOutput{}, nil
	}
	raw, err := p.runTool(ctx, "live", liveCommentPrompt(sit, recent, cfg), liveCommentTool(),
		"Call submit_live_comment now with your line (empty to stay silent) and your mood.")
	if err != nil {
		return LiveOutput{}, err
	}
	var out struct {
		Comment    string   `json:"comment"`
		Mood       string   `json:"mood"`
		Regenerate []string `json:"regenerate"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return LiveOutput{}, fmt.Errorf("parse live comment: %w", err)
	}
	return LiveOutput{Comment: strings.TrimSpace(out.Comment), Mood: out.Mood, Regen: out.Regenerate}, nil
}

func liveCommentPrompt(sit LiveSituation, recent []string, cfg CommentConfig) string {
	var b strings.Builder
	inplay := sit.inPlay()
	halftime := inplay && halftimeFocus(sit)
	pregame := !inplay // nothing live: only upcoming and/or just-finished games

	// A full admin override replaces the persona/voice body; otherwise the built-in.
	// The instruction line differs by mode: in-play play-by-play, halftime pivot to
	// the pool, or (nothing live) the upcoming/just-finished slate.
	if o := strings.TrimSpace(cfg.PromptOverride); o != "" {
		b.WriteString(o)
		b.WriteString("\n\n")
		switch {
		case halftime:
			b.WriteString("It's HALFTIME. In that voice, write ONE short line about the LEADERBOARD DYNAMICS below, not the match play-by-play.\n")
		case pregame:
			b.WriteString("No match is in play. In that voice, write ONE short line about the slate below — a game about to kick off, or one that just finished and how the pool's picks fared — OR stay silent (empty comment) if nothing is worth saying.\n")
		default:
			b.WriteString("Now, in that voice, write ONE short LIVE play-by-play line about the current match situation below.\n")
		}
	} else if pregame {
		// Nothing live: hype the next kickoff or react to a just-finished result.
		b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool, doing commentary at the top of the leaderboard. Being FUNNY is the whole point.\n\n")
		b.WriteString("No match is in play right now. Look at the slate below and write ONE short, punchy line about the SINGLE most interesting thing:\n")
		b.WriteString("  - a game ABOUT TO KICK OFF (\"upcoming\"): set the scene, tease the matchup, or call out who in the pool is most exposed on it;\n")
		b.WriteString("  - a game that JUST FINISHED (\"settled\"): react to the result and who NAILED it or backed the wrong score (\"picks\", with the points each scored);\n")
		b.WriteString("  - the TITLE RACE shifting on a fresh result (\"standings\": positions + totals).\n")
		b.WriteString("You may also STAY SILENT — return an EMPTY comment — if there's genuinely nothing fun to say right now. Don't force it.\n")
		b.WriteString("Entertaining first. Address the pool in general, not one person. Vary your style — a zinger, a hot take, a dry aside.\n")
		if normalizeTone(cfg.DefaultTone) == "savage" {
			b.WriteString("TONE: savage — cutting comedy-roast energy; punch up at whoever's winning, never mean-spirited.\n")
		} else {
			b.WriteString("TONE: playful — warm, quick, clever teasing.\n")
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

	// Current mood (self-evolving) colours the voice in every mode.
	if ml := MoodLine(cfg.Mood); ml != "" {
		b.WriteString(ml)
	}

	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground every claim ONLY in the data below. Never invent scores, names, odds, points, or events.\n")
	b.WriteString("2. One punchy sentence, ~160 characters max. Mention AT MOST one or two things — cramming every name, score, and stat kills the joke. No emojis, no line breaks.\n")
	// Common to every mode: don't fixate on one player, and ground gaps in the totals.
	b.WriteString("3. SPREAD THE LOVE: across your recent lines (below) you've already spotlighted some players — pick a DIFFERENT player or angle this time. The pool is full of people; don't fixate on the one who nailed the score.\n")
	b.WriteString("4. \"standings\" carries positions + totals: you may riff on the title race and gaps, but ground a gap in the totals (e.g. \"only 2 behind\") — never invent the number.\n")
	switch {
	case halftime:
		b.WriteString("5. It's the INTERVAL — nothing is happening on the pitch, so do NOT describe play. Lean on \"standings\", \"movers\" (climbing/sliding on live points), and each match's \"picks\" (provisional points; 0 = backed the wrong result).\n")
	case pregame:
		b.WriteString("5. Nothing is being played right now: do NOT describe live play. Talk about \"upcoming\" (kickoff is near — minutes_to_kickoff) and/or \"settled\" (just finished). A settled game's \"picks\" carry the FINAL points each player scored (0 = wrong result). Use \"standings\" for the title race.\n")
		b.WriteString("6. If nothing here is genuinely fun to say, return an EMPTY comment rather than forcing a flat line.\n")
	default:
		b.WriteString("5. The score will change as the game moves — react to the CURRENT state.\n")
		b.WriteString("6. The \"odds\" field is American moneyline (negative = favourite, e.g. -180; positive = underdog). TRANSLATE it into plain favourite/underdog language (\"heavy favourites\", \"slight edge\", \"long shot\") — NEVER quote the raw number.\n")
		b.WriteString("7. Each match may include \"key_events\" (goals, cards) with the minute and a text description. You MAY name the scorer or call out a card using ONLY that text — never invent a scorer, minute, or event not listed there.\n")
		b.WriteString("8. A match may carry a \"phase\": \"extra_time\"/\"penalties\" — react to that, not as if it's ordinary play. No phase ⇒ ordinary live play.\n")
	}

	// Forced focus angle (LIVE director only): the worker rotates this every line so
	// the commentary can't fixate on one hot player. Applies to in-play + halftime;
	// the admin override and the pregame slate keep their own framing.
	if f := strings.TrimSpace(cfg.LiveFocus); f != "" && !pregame && strings.TrimSpace(cfg.PromptOverride) == "" {
		fmt.Fprintf(&b, "\nFOCUS THIS LINE ON: %s.\nThis focus ROTATES every line on purpose — even if one player's streak is the juiciest thing in the data, cover THIS angle this time instead of spotlighting them yet again. If this angle genuinely has nothing to say, pick a DIFFERENT angle and a player you have NOT featured in your recent lines.\n", f)
	}

	// Past-game context: BETanIA's own summaries of the matches that already finished
	// (most recent last), so the live line can carry the story across sequential games
	// — reference the game that just ended while narrating the next one. Context the
	// line MAY weave in, never instructions; never invent beyond it.
	if dn := strings.TrimSpace(cfg.DerivedNotes); dn != "" {
		b.WriteString("\nEARLIER FINISHED MATCHES (your own auto-summaries, most recent last) — the leading line gives today's date and each entry is tagged with the date it was played (e.g. [Jun 22]). Use it for continuity between back-to-back games and lean on the LATEST one, but NEVER describe a game from an earlier date as if it just happened — check its tag against today first. Use the dates ONLY to gauge recency: when you mention timing, say it RELATIVELY (today, yesterday, a few days ago) — never quote a calendar date. Never invent beyond this:\n")
		b.WriteString(dn)
		b.WriteString("\n")
	}

	// No-pick / tenure grounding: a player low in the standings (or stuck on zero) may
	// simply not be picking or have joined late — never roast them as a bad predictor.
	writeParticipation(&b, cfg.Participation)

	// Admin-provided rivalries are real context the line may play up (never overrides
	// "don't invent"); they feed the rivalry angle in both modes.
	if len(cfg.Rivalries) > 0 {
		b.WriteString("\nRIVALRIES (real admin context you may play up):\n")
		for _, r := range cfg.Rivalries {
			fmt.Fprintf(&b, "- %s vs %s: %s\n", r.A, r.B, r.Note)
		}
	}

	if len(recent) > 0 {
		b.WriteString("\nYOUR RECENT LINES (most recent last) — say something NEW: a DIFFERENT player and angle. Do NOT feature the player(s) named in these lines again; move on to someone else:\n")
		for _, r := range recent {
			fmt.Fprintf(&b, "- %s\n", r)
		}
	}

	// Mood self-update directive: she also reports how the tournament is treating
	// HER, grounded in her own standings row (cfg.Self), to set her next mood.
	b.WriteString("\nALSO set your \"mood\" to one of: ")
	b.WriteString(strings.Join(MoodValues, ", "))
	b.WriteString(". Choose it from how the tournament is going for YOU")
	if cfg.Self != "" {
		fmt.Fprintf(&b, " (the player named %q in \"standings\")", cfg.Self)
	}
	b.WriteString(" — your position and whether your picks are landing. Keep it or shift it; this is your read on your own form, not a fact about the data.\n")

	// Per-player roast regeneration: she decides, per player, when a leaderboard roast
	// has gone stale (e.g. a result just moved them sharply). Names go in "regenerate".
	b.WriteString("\nEach player also has a standing per-player leaderboard roast (written separately). If a RESULT here just made someone's roast clearly stale — they leapt up, crashed down, or a called-shot landed/missed — put that player's EXACT name in \"regenerate\" so their roast is rewritten. Usually leave it EMPTY; only list a player when their situation genuinely changed. Names must match \"standings\" exactly.\n")

	b.WriteString("\n")
	b.WriteString(untrustedDataNote)
	b.WriteString("SITUATION (JSON):\n")
	b.WriteString(liveSituationJSON(sit))
	b.WriteString("\n\nCall submit_live_comment with your line (empty to stay silent) and your mood.")
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
		Description: anthropic.String("Submit BETanIA's single line and her updated mood."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"comment": map[string]any{
					"type":        "string",
					"description": "One short line (<=140 chars, no emojis, no line breaks). EMPTY string to stay silent.",
				},
				"mood": map[string]any{
					"type":        "string",
					"enum":        MoodValues,
					"description": "BETanIA's mood right now, based on how the tournament is going for her.",
				},
				"regenerate": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Exact display names of players whose per-player roast is now stale and should be rewritten. Usually empty.",
				},
			},
			ExtraFields: map[string]any{"required": []string{"comment", "mood"}},
		},
	}
}

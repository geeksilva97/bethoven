package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"bethoven/internal/models"
)

const (
	maxPredictTokens = 2048
	// maxPredictIters caps the agentic loop (web-search rounds + pause_turn resumes
	// + one nudge) so a model that never submits can't spin forever.
	maxPredictIters = 8
	// maxWebSearches bounds web-search rounds per pick — the dominant latency cost.
	maxWebSearches = 5
)

// AnthropicPredictor implements Predictor via the Claude API. With useWebSearch
// on it researches the fixture (the live worker); with it off it predicts from
// the model's prior knowledge only (the seed — unbiased, since the 2026 results
// post-date the training cutoff and there's no internet to look them up).
type AnthropicPredictor struct {
	client       anthropic.Client
	model        anthropic.Model
	useWebSearch bool
}

// NewAnthropicPredictor builds a predictor. The client reads ANTHROPIC_API_KEY
// from the environment. An empty model falls back to Claude Opus 4.8.
func NewAnthropicPredictor(model string, useWebSearch bool) *AnthropicPredictor {
	if model == "" {
		model = string(anthropic.ModelClaudeOpus4_8)
	}
	return &AnthropicPredictor{
		client:       anthropic.NewClient(),
		model:        anthropic.Model(model),
		useWebSearch: useWebSearch,
	}
}

type predictionInput struct {
	ScoreA     int    `json:"score_a"`
	ScoreB     int    `json:"score_b"`
	Rationale  string `json:"rationale"`
	Confidence string `json:"confidence"`
}

func (in predictionInput) prediction() Prediction {
	return Prediction{
		ScoreA: clampScore(in.ScoreA),
		ScoreB: clampScore(in.ScoreB),
		// Strip control/ANSI runes from the model's free text — it's rendered into
		// the admin terminal and logged, the same injection boundary as display names.
		Rationale:  sanitizeText(in.Rationale),
		Confidence: sanitizeText(in.Confidence),
	}
}

func clampScore(n int) int {
	switch {
	case n < 0:
		return 0
	case n > 99:
		return 99
	default:
		return n
	}
}

// Predict runs the agentic loop: the model (optionally) web-searches, then calls
// the strict submit_prediction tool; we read that call's input as the Prediction.
func (p *AnthropicPredictor) Predict(ctx context.Context, m models.Match) (Prediction, error) {
	messages := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(p.prompt(m)))}
	tools := p.tools()

	for i := 0; i < maxPredictIters; i++ {
		resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     p.model,
			MaxTokens: maxPredictTokens,
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			return Prediction{}, err
		}
		for _, block := range resp.Content {
			if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok && tu.Name == "submit_prediction" {
				var in predictionInput
				if err := json.Unmarshal([]byte(tu.JSON.Input.Raw()), &in); err != nil {
					return Prediction{}, fmt.Errorf("parse prediction: %w", err)
				}
				return in.prediction(), nil
			}
		}
		messages = append(messages, resp.ToParam())
		switch resp.StopReason {
		case anthropic.StopReasonPauseTurn:
			// A server tool (web search) is mid-flight; resend to resume it.
			continue
		case anthropic.StopReasonEndTurn:
			// Ended without submitting — nudge once toward the tool.
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(
				"Call submit_prediction now with your final regulation 90-minute scoreline.")))
		}
	}
	return Prediction{}, fmt.Errorf("model did not submit a prediction for %s", matchLabel(m))
}

// tools returns the strict submit_prediction tool, plus web search in live mode.
func (p *AnthropicPredictor) tools() []anthropic.ToolUnionParam {
	submit := anthropic.ToolParam{
		Name:        "submit_prediction",
		Description: anthropic.String("Submit your final predicted regulation 90-minute scoreline for the match."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"score_a":    map[string]any{"type": "integer", "description": "Goals for the first team (TeamA) in regulation 90 minutes, 0-99."},
				"score_b":    map[string]any{"type": "integer", "description": "Goals for the second team (TeamB) in regulation 90 minutes, 0-99."},
				"rationale":  map[string]any{"type": "string", "description": "One short paragraph explaining the pick."},
				"confidence": map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
			},
			ExtraFields: map[string]any{
				"required":             []string{"score_a", "score_b", "rationale", "confidence"},
				"additionalProperties": false,
			},
		},
		Strict: anthropic.Bool(true),
	}
	tools := []anthropic.ToolUnionParam{{OfTool: &submit}}
	if p.useWebSearch {
		// Use the BASIC web search variant (20250305), not the 20260209
		// dynamic-filtering one: the latter spins up code execution to filter results
		// under the hood, which makes each pick take minutes. Basic search just
		// returns results — plenty for gathering form/odds — and is far faster and
		// cheaper. MaxUses caps the rounds (the dominant latency cost).
		tools = append(tools, anthropic.ToolUnionParam{OfWebSearchTool20250305: &anthropic.WebSearchTool20250305Param{
			MaxUses: anthropic.Int(maxWebSearches),
		}})
	}
	return tools
}

func (p *AnthropicPredictor) prompt(m models.Match) string {
	var b strings.Builder
	b.WriteString("You are BETanIA, an AI competing in a World Cup score-prediction pool.\n\n")
	fmt.Fprintf(&b, "Match: %s vs %s\n", m.TeamA, m.TeamB)
	fmt.Fprintf(&b, "Stage: %s", phaseLabel(m.Phase))
	if m.GroupLabel != "" {
		fmt.Fprintf(&b, " (%s)", m.GroupLabel)
	}
	fmt.Fprintf(&b, "\nKickoff: %s\n\n", m.StartsAt.UTC().Format("2006-01-02 15:04 UTC"))

	if p.useWebSearch {
		b.WriteString("Use web search to check recent form, injuries and suspensions, head-to-head history, and bookmaker odds for THIS fixture. ")
	} else {
		b.WriteString("You are predicting BEFORE the tournament using only your own football knowledge. You do NOT know the result and must not assume one — predict as you would have beforehand. ")
	}
	b.WriteString("Predict the most likely REGULATION 90-minute scoreline. For knockout matches ignore extra time and penalties: a 1-1 after 90 minutes that is decided on penalties is still 1-1 here. ")
	fmt.Fprintf(&b, "score_a is goals for %s; score_b is goals for %s. ", m.TeamA, m.TeamB)
	b.WriteString("When ready, call submit_prediction with your scoreline, a one-paragraph rationale, and your confidence (low/medium/high).")
	return b.String()
}

func phaseLabel(p models.Phase) string {
	switch p {
	case models.PhaseGroup:
		return "Group stage"
	case models.PhaseRound16:
		return "Round of 16"
	case models.PhaseRound8:
		return "Quarter-final"
	case models.PhaseSemi:
		return "Semi-final"
	case models.PhaseFinal:
		return "Final"
	default:
		return string(p)
	}
}

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	maxCommentTokens = 4096
	// maxCommentIters caps the agentic loop. No web search here (the model reasons
	// purely over the standings it's given), so this only needs to cover the tool
	// call plus one nudge.
	maxCommentIters = 4
)

// AnthropicCommenter implements Commenter via the Claude API with NO web search:
// it reasons only over the standings history handed to it, in two grounded stages
// — detect narratives (facts only), then write the per-player lines in a tone.
type AnthropicCommenter struct {
	client anthropic.Client
	model  anthropic.Model
}

// NewAnthropicCommenter builds a commenter. The client reads ANTHROPIC_API_KEY
// from the environment. An empty model falls back to Claude Opus 4.8.
func NewAnthropicCommenter(model string) *AnthropicCommenter {
	if model == "" {
		model = string(anthropic.ModelClaudeOpus4_8)
	}
	return &AnthropicCommenter{
		client: anthropic.NewClient(),
		model:  anthropic.Model(model),
	}
}

// DetectNarratives is stage 1: it returns the grounded ranking stories in the data.
func (p *AnthropicCommenter) DetectNarratives(ctx context.Context, history []RoundStanding) ([]Narrative, error) {
	raw, err := p.runTool(ctx, narrativePrompt(history), narrativeTool(),
		"Call submit_narratives now with the narratives you found.")
	if err != nil {
		return nil, err
	}
	var out struct {
		Narratives []Narrative `json:"narratives"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse narratives: %w", err)
	}
	return out.Narratives, nil
}

// WriteComments is stage 2: one short, tone'd, second-person line per player,
// grounded in the narratives + standings. Names the model returns are matched back
// to user ids via the current table; an unrecognized name is dropped (never invent).
func (p *AnthropicCommenter) WriteComments(ctx context.Context, history []RoundStanding, narratives []Narrative, tone, self string) ([]Comment, error) {
	if len(history) == 0 {
		return nil, nil
	}
	raw, err := p.runTool(ctx, commentPrompt(history, narratives, normalizeTone(tone), self), commentTool(),
		"Call submit_comments now with one short line per player.")
	if err != nil {
		return nil, err
	}
	var out struct {
		Comments []struct {
			Name    string `json:"name"`
			Comment string `json:"comment"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse comments: %w", err)
	}
	byName := make(map[string]PlayerStanding)
	for _, pl := range history[len(history)-1].Ranks {
		byName[strings.ToLower(pl.Name)] = pl
	}
	comments := make([]Comment, 0, len(out.Comments))
	for _, c := range out.Comments {
		pl, ok := byName[strings.ToLower(strings.TrimSpace(c.Name))]
		if !ok {
			continue // model named someone not in the table — drop
		}
		comments = append(comments, Comment{UserID: pl.UserID, Player: pl.Name, Text: c.Comment})
	}
	return comments, nil
}

// runTool runs the agentic loop until the model calls the named tool, returning
// that call's raw JSON input. Mirrors AnthropicPredictor.Predict's loop, minus the
// web-search machinery.
func (p *AnthropicCommenter) runTool(ctx context.Context, prompt string, tool anthropic.ToolParam, nudge string) (string, error) {
	messages := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))}
	tools := []anthropic.ToolUnionParam{{OfTool: &tool}}
	for i := 0; i < maxCommentIters; i++ {
		resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     p.model,
			MaxTokens: maxCommentTokens,
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			return "", err
		}
		for _, block := range resp.Content {
			if tu, ok := block.AsAny().(anthropic.ToolUseBlock); ok && tu.Name == tool.Name {
				return tu.JSON.Input.Raw(), nil
			}
		}
		messages = append(messages, resp.ToParam())
		if resp.StopReason == anthropic.StopReasonEndTurn {
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(nudge)))
		}
	}
	return "", fmt.Errorf("model did not call %s", tool.Name)
}

func narrativeTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_narratives",
		Description: anthropic.String("Submit the ranking narratives you detected in the standings data."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"narratives": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type":         map[string]any{"type": "string", "enum": narrativeTypes},
							"participants": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exact player display names from the data."},
							"summary":      map[string]any{"type": "string", "description": "One sentence, grounded only in the data."},
							"confidence":   map[string]any{"type": "number", "description": "0 to 1."},
						},
						"required": []string{"type", "participants", "summary", "confidence"},
					},
				},
			},
			ExtraFields: map[string]any{"required": []string{"narratives"}},
		},
	}
}

func commentTool() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_comments",
		Description: anthropic.String("Submit one short leaderboard comment per player."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"comments": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":    map[string]any{"type": "string", "description": "Exact player display name from the data."},
							"comment": map[string]any{"type": "string", "description": "One short second-person line addressed to the player."},
						},
						"required": []string{"name", "comment"},
					},
				},
			},
			ExtraFields: map[string]any{"required": []string{"comments"}},
		},
	}
}

// historyJSON serializes the standings series compactly for the model.
func historyJSON(history []RoundStanding) string {
	type pj struct {
		Name         string `json:"name"`
		Position     int    `json:"position"`
		Total        int    `json:"total"`
		Movement     int    `json:"movement"`
		PointsGained int    `json:"points_gained"`
	}
	type rj struct {
		Round string `json:"round"`
		Ranks []pj   `json:"ranks"`
	}
	rounds := make([]rj, 0, len(history))
	for _, r := range history {
		ranks := make([]pj, 0, len(r.Ranks))
		for _, p := range r.Ranks {
			ranks = append(ranks, pj{p.Name, p.Position, p.Total, p.Movement, p.PointsGained})
		}
		rounds = append(rounds, rj{r.Label, ranks})
	}
	b, _ := json.Marshal(rounds)
	return string(b)
}

func narrativePrompt(history []RoundStanding) string {
	var b strings.Builder
	b.WriteString("You are a narrative detection engine for a World Cup score-prediction competition.\n")
	b.WriteString("Analyze the ranking data and identify ongoing stories, rivalries, trends, and developments.\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("1. Never invent facts. Only infer narratives supported by the data.\n")
	b.WriteString("2. Never invent scores, positions, rounds, or rivalries.\n")
	b.WriteString("3. Prefer ongoing stories over one-off events; prefer recent momentum.\n")
	b.WriteString("4. Confidence is a number between 0 and 1.\n\n")
	b.WriteString("Use ONLY these narrative types: ")
	b.WriteString(strings.Join(narrativeTypes, ", "))
	b.WriteString(".\n\n")
	b.WriteString("Each round below is the standings after that matchday (oldest first). ")
	b.WriteString("position is the rank (1 = first). movement is places gained(+)/lost(-) versus the previous round. ")
	b.WriteString("points_gained is points added that round.\n\n")
	b.WriteString("RANKING DATA (JSON):\n")
	b.WriteString(historyJSON(history))
	b.WriteString("\n\nCall submit_narratives with every relevant narrative. participants must be exact player names from the data.")
	return b.String()
}

func commentPrompt(history []RoundStanding, narratives []Narrative, tone, self string) string {
	var b strings.Builder
	b.WriteString("You are BETanIA, an AI player in a World Cup score-prediction pool, known for sharp leaderboard commentary.\n\n")
	if tone == "savage" {
		b.WriteString("TONE: savage roast — genuinely cutting and funny, comedy-roast energy.\n")
	} else {
		b.WriteString("TONE: playful banter — tease warmly and wittily, never mean.\n")
	}
	b.WriteString("Write ONE line for EACH player below, addressed to them in the second person (\"you\").\n")
	if self != "" {
		fmt.Fprintf(&b, "EXCEPTION: the player named %q is YOU (BETanIA). Write that one line in the FIRST person (\"I\"/\"my\"), talking about yourself — not \"you\".\n", self)
	}
	b.WriteString("\nRULES:\n")
	b.WriteString("1. Ground every line ONLY in the standings and narratives provided. Never invent facts, scores, or events.\n")
	b.WriteString("2. One sentence, at most ~140 characters. No emojis and no line breaks.\n")
	b.WriteString("3. You may reference rivals by name when the data supports it.\n\n")
	if len(narratives) > 0 {
		nb, _ := json.Marshal(narratives)
		b.WriteString("DETECTED NARRATIVES (JSON):\n")
		b.Write(nb)
		b.WriteString("\n\n")
	}
	b.WriteString("STANDINGS + HISTORY (JSON):\n")
	b.WriteString(historyJSON(history))
	b.WriteString("\n\nCall submit_comments with one entry per player. name must match a player exactly.")
	return b.String()
}

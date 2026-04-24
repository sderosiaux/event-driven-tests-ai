package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
)

// LLMJudge scores pairs via the Anthropic Messages API.
//
// Prompt shape: system = rubric (verbatim from the scenario), user = JSON
// payload {input, output, meta} + instruction to emit a single JSON object
// {score: <number>, rationale: "<why>"}. We parse the first JSON object out of
// the response, so the model may wrap it in prose without breaking parsing.
type LLMJudge struct {
	client anthropic.Client
	model  anthropic.Model
}

// NewLLMJudge wires an Anthropic client from the ANTHROPIC_API_KEY env var.
// An explicit apiKey wins over the env; pass "" to fall back to ANTHROPIC_API_KEY.
// modelOverride lets the scenario pick a different model than the default
// claude-opus-4-7; pass "" to keep the default.
func NewLLMJudge(apiKey, modelOverride string) *LLMJudge {
	opts := []option.RequestOption{}
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	model := anthropic.Model("claude-opus-4-7")
	if modelOverride != "" {
		model = anthropic.Model(modelOverride)
	}
	return &LLMJudge{client: anthropic.NewClient(opts...), model: model}
}

// Score renders the rubric + pair into a prompt, asks Claude for a verdict,
// and extracts the numeric score + rationale.
func (j *LLMJudge) Score(ctx context.Context, ev scenario.Eval, p Pair) (Score, error) {
	if ev.Judge == nil {
		return Score{}, fmt.Errorf("eval: no judge block on eval %q", ev.Name)
	}
	model := j.model
	if ev.Judge.Model != "" {
		model = anthropic.Model(ev.Judge.Model)
	}

	userPayload, err := json.MarshalIndent(map[string]any{
		"input":  p.Input,
		"output": p.Output,
		"meta":   p.Meta,
	}, "", "  ")
	if err != nil {
		return Score{}, fmt.Errorf("eval: marshal pair: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := j.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{{
			Text: systemPromptFor(ev),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(string(userPayload))),
		},
	})
	if err != nil {
		return Score{}, fmt.Errorf("eval: anthropic api: %w", err)
	}

	raw := extractText(resp)
	value, rationale, err := parseVerdict(raw)
	if err != nil {
		return Score{Err: err, Rationale: raw}, nil
	}
	return Score{Value: value, Rationale: rationale}, nil
}

// systemPromptFor wraps the user-supplied rubric with the contract the judge
// must follow. We pin the output shape so parseVerdict can rely on it.
func systemPromptFor(ev scenario.Eval) string {
	version := ev.Judge.RubricVersion
	if version == "" {
		version = "v0"
	}
	return fmt.Sprintf(`You are an automated judge for an event-driven scenario.
Rubric (version %s):
%s

You will receive a JSON object with fields:
- input:  the event seeded by the test harness
- output: the agent-under-test's response event
- meta:   run context (scenario name, iteration, etc.)

Return EXACTLY one JSON object, no prose, in this shape:
{"score": <number>, "rationale": "<one or two sentences>"}

Scores must be numeric. Higher is better. Do not wrap the JSON in markdown.`, version, ev.Judge.Rubric)
}

// extractText joins every text block from the response into a single string.
// A well-behaved judge emits exactly one text block.
func extractText(m *anthropic.Message) string {
	var b strings.Builder
	for _, block := range m.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			b.WriteString(v.Text)
		}
	}
	return b.String()
}

// verdictRE captures the first JSON object with a `score` field. We accept
// either a bare object or one embedded in prose/Markdown fences.
var verdictRE = regexp.MustCompile(`(?s)\{[^{}]*"score"[^{}]*\}`)

func parseVerdict(text string) (float64, string, error) {
	match := verdictRE.FindString(text)
	if match == "" {
		return 0, "", fmt.Errorf("eval: no JSON verdict found in response: %q", truncate(text, 200))
	}
	var parsed struct {
		Score     any    `json:"score"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(match), &parsed); err != nil {
		return 0, "", fmt.Errorf("eval: decode verdict: %w", err)
	}
	value, err := coerceScore(parsed.Score)
	if err != nil {
		return 0, "", err
	}
	return value, parsed.Rationale, nil
}

func coerceScore(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, fmt.Errorf("eval: score is not numeric: %q", n)
		}
		return f, nil
	}
	return 0, fmt.Errorf("eval: unsupported score type %T", v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

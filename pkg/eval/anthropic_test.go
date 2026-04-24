package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVerdictBareJSON(t *testing.T) {
	v, rationale, err := parseVerdict(`{"score": 4.2, "rationale": "close enough"}`)
	require.NoError(t, err)
	assert.InDelta(t, 4.2, v, 1e-9)
	assert.Equal(t, "close enough", rationale)
}

func TestParseVerdictWrappedInProse(t *testing.T) {
	txt := "Here is the verdict:\n```json\n{\"score\":5,\"rationale\":\"exact match\"}\n```\n"
	v, r, err := parseVerdict(txt)
	require.NoError(t, err)
	assert.Equal(t, 5.0, v)
	assert.Equal(t, "exact match", r)
}

func TestParseVerdictStringScoreCoerces(t *testing.T) {
	v, _, err := parseVerdict(`{"score": "4.7", "rationale": "ok"}`)
	require.NoError(t, err)
	assert.InDelta(t, 4.7, v, 1e-9)
}

func TestParseVerdictNoJSONErrors(t *testing.T) {
	_, _, err := parseVerdict("I decline to score this.")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no JSON verdict")
}

func TestParseVerdictPicksFirstObjectWithScore(t *testing.T) {
	// Some rubric text before, then a real verdict.
	txt := `First the rubric says: {"note":"n/a"}. Now the verdict: {"score": 3, "rationale": "meh"}.`
	v, _, err := parseVerdict(txt)
	require.NoError(t, err)
	assert.Equal(t, 3.0, v)
}

// Codex P1 #10: unrelated score fields inside nested or leading objects must
// not be picked up as verdicts. Tokeniser walks balanced braces rather than
// regex-matching the first brace-wrapped "score".
func TestParseVerdictSkipsNestedScoreInRubricEcho(t *testing.T) {
	txt := `Rubric echo: {"criteria":[{"score_hint":"1-5"}], "note":"ignore this"}.
Actual verdict: {"score": 4.2, "rationale": "ok"}`
	v, _, err := parseVerdict(txt)
	require.NoError(t, err)
	assert.InDelta(t, 4.2, v, 1e-9)
}

func TestParseVerdictHandlesStringWithEscapedBraces(t *testing.T) {
	txt := `{"rationale": "the JSON \"{score}\" isn't a real score", "note": "skip"}
{"score": 5, "rationale": "final"}`
	v, _, err := parseVerdict(txt)
	require.NoError(t, err)
	assert.Equal(t, 5.0, v)
}

package checks_test

import (
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/checks"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEval(t *testing.T, seed func(s *events.MemStore)) *checks.Evaluator {
	t.Helper()
	store := events.NewMemStore(0)
	if seed != nil {
		seed(store)
	}
	e, err := checks.NewEvaluator(store)
	require.NoError(t, err)
	return e
}

func TestStandardCEL(t *testing.T) {
	e := newEval(t, nil)
	v, err := e.Evaluate("1 + 2 == 3")
	require.NoError(t, err)
	assert.Equal(t, true, v)
}

func TestDurationLiteral(t *testing.T) {
	e := newEval(t, nil)
	v, err := e.EvaluateBool("duration('200ms') < duration('1s')")
	require.NoError(t, err)
	assert.True(t, v)
}

func TestStreamReturnsEvents(t *testing.T) {
	e := newEval(t, func(s *events.MemStore) {
		s.Append(events.Event{Stream: "orders", Key: "1", Ts: time.Unix(1, 0)})
		s.Append(events.Event{Stream: "orders", Key: "2", Ts: time.Unix(2, 0)})
		s.Append(events.Event{Stream: "other", Key: "x"})
	})
	v, err := e.Evaluate("size(stream('orders'))")
	require.NoError(t, err)
	assert.Equal(t, int64(2), v)
}

func TestPercentileDoubles(t *testing.T) {
	e := newEval(t, nil)
	// p50 of [10,20,30,40,50] = 30
	v, err := e.Evaluate("percentile([10.0, 20.0, 30.0, 40.0, 50.0], 50)")
	require.NoError(t, err)
	assert.InDelta(t, 30.0, v.(float64), 1e-9)
}

func TestPercentileDoublesP99(t *testing.T) {
	e := newEval(t, nil)
	v, err := e.Evaluate("percentile([1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0], 99)")
	require.NoError(t, err)
	// rank = 0.99 * 9 = 8.91 → low=8 (=9.0), high=9 (=10.0), frac=0.91
	// result = 9.0 + 0.91 * (10.0 - 9.0) = 9.91
	assert.InDelta(t, 9.91, v.(float64), 1e-9)
}

// Pin the Type 7 algorithm precisely so a future change is caught.
func TestPercentileType7Precise(t *testing.T) {
	e := newEval(t, nil)
	cases := []struct {
		expr string
		want float64
	}{
		{"percentile([10.0, 20.0], 99)", 19.9},
		{"percentile([10.0, 20.0, 30.0], 50)", 20.0},
		{"percentile([1.0, 2.0, 3.0, 4.0], 75)", 3.25},
	}
	for _, tc := range cases {
		v, err := e.Evaluate(tc.expr)
		require.NoError(t, err, tc.expr)
		assert.InDelta(t, tc.want, v.(float64), 1e-9, tc.expr)
	}
}

func TestRateBoolList(t *testing.T) {
	e := newEval(t, nil)
	v, err := e.Evaluate("rate([true, false, true, true, false])")
	require.NoError(t, err)
	assert.InDelta(t, 0.6, v.(float64), 1e-9)
}

func TestRateEmpty(t *testing.T) {
	e := newEval(t, nil)
	v, err := e.Evaluate("rate([])")
	require.NoError(t, err)
	assert.Equal(t, 0.0, v.(float64))
}

func TestEvaluateBoolRejectsNonBool(t *testing.T) {
	e := newEval(t, nil)
	_, err := e.EvaluateBool("1 + 2")
	require.Error(t, err)
}

func TestCompileError(t *testing.T) {
	e := newEval(t, nil)
	_, err := e.Evaluate("this is not valid CEL")
	require.Error(t, err)
}

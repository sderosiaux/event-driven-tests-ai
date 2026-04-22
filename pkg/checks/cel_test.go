package checks_test

import (
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/checks"
	"github.com/event-driven-tests-ai/edt/pkg/events"
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
	// p99 of 10 evenly spaced = ~9.91 with linear interpolation
	assert.InDelta(t, 9.91, v.(float64), 0.01)
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

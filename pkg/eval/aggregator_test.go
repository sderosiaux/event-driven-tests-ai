package eval_test

import (
	"math"
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAggregateAvg(t *testing.T) {
	a := eval.NewAggregate("avg")
	for _, v := range []float64{1, 2, 3, 4, 5} {
		a.Add(eval.Score{Value: v})
	}
	assert.InDelta(t, 3.0, a.Value(), 1e-9)
	assert.Equal(t, 5, a.Count())
}

func TestAggregateEmptyIsNaN(t *testing.T) {
	a := eval.NewAggregate("avg")
	assert.True(t, math.IsNaN(a.Value()))
}

func TestAggregateDropsErrors(t *testing.T) {
	a := eval.NewAggregate("avg")
	a.Add(eval.Score{Value: 4})
	a.Add(eval.Score{Err: assert.AnError})
	a.Add(eval.Score{Value: 5})
	assert.InDelta(t, 4.5, a.Value(), 1e-9)
	assert.Equal(t, 2, a.Count())
}

func TestAggregateKinds(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := map[string]float64{
		"min": 1, "max": 10, "p50": 5.5, "p95": 9.55, "avg": 5.5,
	}
	for kind, want := range cases {
		a := eval.NewAggregate(kind)
		for _, v := range vals {
			a.Add(eval.Score{Value: v})
		}
		assert.InDelta(t, want, a.Value(), 1e-9, "kind=%s", kind)
	}
}

func TestAggregateUnknownKindYieldsNaN(t *testing.T) {
	a := eval.NewAggregate("geomean")
	a.Add(eval.Score{Value: 1})
	assert.True(t, math.IsNaN(a.Value()))
}

func TestCheckThreshold(t *testing.T) {
	cases := []struct {
		observed float64
		expr     string
		want     bool
	}{
		{4.5, ">= 4.2", true},
		{4.0, ">= 4.2", false},
		{3.0, "< 4", true},
		{10, "> 9", true},
		{5, "== 5", true},
		{5, "!= 5", false},
	}
	for _, c := range cases {
		got, err := eval.CheckThreshold(c.observed, c.expr)
		require.NoError(t, err, c.expr)
		assert.Equal(t, c.want, got, "%v %s", c.observed, c.expr)
	}
}

func TestCheckThresholdBadExprErrors(t *testing.T) {
	_, err := eval.CheckThreshold(5, "approximately 4")
	require.Error(t, err)
	_, err = eval.CheckThreshold(5, ">= not-a-number")
	require.Error(t, err)
}

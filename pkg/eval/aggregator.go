package eval

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Aggregate collects Score values and reduces them to a single number with a
// named aggregate function (avg, p50, p95, min, max). Errors (Score.Err != nil)
// are skipped.
type Aggregate struct {
	Kind string
	vals []float64
}

// NewAggregate accepts "avg", "p50", "p95", "min", "max" (case-insensitive).
// Empty kind defaults to "avg".
func NewAggregate(kind string) *Aggregate {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "avg"
	}
	return &Aggregate{Kind: kind}
}

// Add appends a successful score. Errors are dropped so they never pull the
// average toward zero.
func (a *Aggregate) Add(s Score) {
	if s.Err != nil {
		return
	}
	a.vals = append(a.vals, s.Value)
}

// Value computes the aggregate. Returns NaN on empty input so callers can
// distinguish no-data from a legitimately-zero score.
func (a *Aggregate) Value() float64 {
	if len(a.vals) == 0 {
		return math.NaN()
	}
	switch a.Kind {
	case "avg", "mean":
		sum := 0.0
		for _, v := range a.vals {
			sum += v
		}
		return sum / float64(len(a.vals))
	case "min":
		out := a.vals[0]
		for _, v := range a.vals[1:] {
			if v < out {
				out = v
			}
		}
		return out
	case "max":
		out := a.vals[0]
		for _, v := range a.vals[1:] {
			if v > out {
				out = v
			}
		}
		return out
	case "p50":
		return percentile(a.vals, 50)
	case "p95":
		return percentile(a.vals, 95)
	case "p99":
		return percentile(a.vals, 99)
	}
	return math.NaN()
}

// Count is the number of non-error samples.
func (a *Aggregate) Count() int { return len(a.vals) }

// thresholdPattern splits the scenario threshold expression (e.g. ">= 4.2")
// into an operator and a right-hand-side number.
var thresholdOps = []string{">=", "<=", "==", "!=", ">", "<"}

// CheckThreshold returns (passed, err). err is non-nil when the threshold
// expression is malformed.
func CheckThreshold(observed float64, expr string) (bool, error) {
	expr = strings.TrimSpace(expr)
	for _, op := range thresholdOps {
		if strings.HasPrefix(expr, op) {
			rhs, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(expr, op)), 64)
			if err != nil {
				return false, fmt.Errorf("eval: threshold rhs %q: %w", expr, err)
			}
			return compare(observed, op, rhs), nil
		}
	}
	return false, fmt.Errorf("eval: threshold must start with one of %v, got %q", thresholdOps, expr)
}

func compare(a float64, op string, b float64) bool {
	switch op {
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case "<":
		return a < b
	case "==":
		return a == b
	case "!=":
		return a != b
	}
	return false
}

func percentile(values []float64, p int) float64 {
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := float64(p) / 100.0 * float64(len(sorted)-1)
	low := int(math.Floor(rank))
	high := int(math.Ceil(rank))
	if low == high {
		return sorted[low]
	}
	frac := rank - float64(low)
	return sorted[low]*(1-frac) + sorted[high]*frac
}

package checks

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// storeFunctions returns the cel.EnvOption list registering all streaming
// operators bound to the given store.
//
// Phase 1 (this commit): stream, percentile, rate.
// Phase 2 (next task):   latency, forall, exists, before.
func storeFunctions(store events.Store) []cel.EnvOption {
	return []cel.EnvOption{
		fnStream(store),
		fnPercentileDouble(),
		fnPercentileDuration(),
		fnRate(),
	}
}

// fnStream registers `stream(name) -> list<map<string, dyn>>` returning all
// events recorded for the given logical stream name (a topic, "http:/path", etc.).
func fnStream(store events.Store) cel.EnvOption {
	return cel.Function("stream",
		cel.Overload("stream_string",
			[]*cel.Type{cel.StringType},
			cel.ListType(cel.MapType(cel.StringType, cel.DynType)),
			cel.UnaryBinding(func(arg ref.Val) ref.Val {
				name, ok := arg.Value().(string)
				if !ok {
					return types.NewErr("stream: expected string, got %T", arg.Value())
				}
				evs := store.Query(name)
				out := make([]any, len(evs))
				for i, e := range evs {
					out[i] = eventToMap(e)
				}
				return types.DefaultTypeAdapter.NativeToValue(out)
			}),
		),
	)
}

// fnPercentileDouble registers `percentile(list<double>, int) -> double`.
func fnPercentileDouble() cel.EnvOption {
	return cel.Function("percentile",
		cel.Overload("percentile_list_double_int",
			[]*cel.Type{cel.ListType(cel.DoubleType), cel.IntType},
			cel.DoubleType,
			cel.BinaryBinding(func(list, p ref.Val) ref.Val {
				vals, err := refValToFloats(list)
				if err != nil {
					return types.NewErr("percentile: %v", err)
				}
				pct, ok := p.Value().(int64)
				if !ok {
					return types.NewErr("percentile: expected int percentile, got %T", p.Value())
				}
				return types.Double(percentile(vals, int(pct)))
			}),
		),
	)
}

// fnPercentileDuration registers `percentile(list<duration>, int) -> duration`.
// Convenient when used with latency() which yields durations.
func fnPercentileDuration() cel.EnvOption {
	return cel.Function("percentile",
		cel.Overload("percentile_list_duration_int",
			[]*cel.Type{cel.ListType(cel.DurationType), cel.IntType},
			cel.DurationType,
			cel.BinaryBinding(func(list, p ref.Val) ref.Val {
				durs, err := refValToDurations(list)
				if err != nil {
					return types.NewErr("percentile: %v", err)
				}
				pct, ok := p.Value().(int64)
				if !ok {
					return types.NewErr("percentile: expected int percentile, got %T", p.Value())
				}
				asNanos := make([]float64, len(durs))
				for i, d := range durs {
					asNanos[i] = float64(d.Nanoseconds())
				}
				v := percentile(asNanos, int(pct))
				return types.Duration{Duration: time.Duration(int64(v))}
			}),
		),
	)
}

// fnRate registers `rate(list<bool>) -> double` — fraction of true values in [0, 1].
// Returns 0.0 on an empty list (caller should treat empty windows explicitly).
func fnRate() cel.EnvOption {
	return cel.Function("rate",
		cel.Overload("rate_list_bool",
			[]*cel.Type{cel.ListType(cel.BoolType)},
			cel.DoubleType,
			cel.UnaryBinding(func(arg ref.Val) ref.Val {
				bools, err := refValToBools(arg)
				if err != nil {
					return types.NewErr("rate: %v", err)
				}
				if len(bools) == 0 {
					return types.Double(0)
				}
				count := 0
				for _, b := range bools {
					if b {
						count++
					}
				}
				return types.Double(float64(count) / float64(len(bools)))
			}),
		),
	)
}

// eventToMap converts an events.Event to a map exposed to CEL.
// The map shape is documented as part of the CEL API contract.
func eventToMap(e events.Event) map[string]any {
	headers := make(map[string]any, len(e.Headers))
	for k, v := range e.Headers {
		headers[k] = v
	}
	return map[string]any{
		"stream":    e.Stream,
		"key":       e.Key,
		"ts":        e.Ts,
		"headers":   headers,
		"payload":   e.Payload,
		"direction": string(e.Direction),
	}
}

// percentile computes the p-th percentile of values using linear interpolation.
// p is in [0, 100]. Empty input returns NaN.
func percentile(values []float64, p int) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	if len(sorted) == 1 {
		return sorted[0]
	}
	// Linear interpolation between adjacent ranks.
	rank := float64(p) / 100.0 * float64(len(sorted)-1)
	low := int(math.Floor(rank))
	high := int(math.Ceil(rank))
	if low == high {
		return sorted[low]
	}
	frac := rank - float64(low)
	return sorted[low]*(1-frac) + sorted[high]*frac
}

// ---- ref.Val converters ----------------------------------------------------

func refValToFloats(v ref.Val) ([]float64, error) {
	raw, ok := v.Value().([]ref.Val)
	if !ok {
		// Fallback: any Go slice the adapter handed back.
		if generic, ok := v.Value().([]any); ok {
			out := make([]float64, len(generic))
			for i, x := range generic {
				f, err := toFloat(x)
				if err != nil {
					return nil, fmt.Errorf("element %d: %w", i, err)
				}
				out[i] = f
			}
			return out, nil
		}
		return nil, fmt.Errorf("expected list, got %T", v.Value())
	}
	out := make([]float64, len(raw))
	for i, e := range raw {
		f, err := toFloat(e.Value())
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out[i] = f
	}
	return out, nil
}

func refValToDurations(v ref.Val) ([]time.Duration, error) {
	raw, ok := v.Value().([]ref.Val)
	if !ok {
		if generic, ok := v.Value().([]any); ok {
			out := make([]time.Duration, len(generic))
			for i, x := range generic {
				d, err := toDuration(x)
				if err != nil {
					return nil, fmt.Errorf("element %d: %w", i, err)
				}
				out[i] = d
			}
			return out, nil
		}
		return nil, fmt.Errorf("expected list, got %T", v.Value())
	}
	out := make([]time.Duration, len(raw))
	for i, e := range raw {
		d, err := toDuration(e.Value())
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out[i] = d
	}
	return out, nil
}

func refValToBools(v ref.Val) ([]bool, error) {
	raw, ok := v.Value().([]ref.Val)
	if !ok {
		if generic, ok := v.Value().([]any); ok {
			out := make([]bool, len(generic))
			for i, x := range generic {
				b, ok := x.(bool)
				if !ok {
					return nil, fmt.Errorf("element %d: expected bool, got %T", i, x)
				}
				out[i] = b
			}
			return out, nil
		}
		return nil, fmt.Errorf("expected list, got %T", v.Value())
	}
	out := make([]bool, len(raw))
	for i, e := range raw {
		b, ok := e.Value().(bool)
		if !ok {
			return nil, fmt.Errorf("element %d: expected bool, got %T", i, e.Value())
		}
		out[i] = b
	}
	return out, nil
}

func toFloat(x any) (float64, error) {
	switch v := x.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("not a number: %T", x)
	}
}

func toDuration(x any) (time.Duration, error) {
	switch v := x.(type) {
	case time.Duration:
		return v, nil
	case int64:
		return time.Duration(v), nil
	default:
		return 0, fmt.Errorf("not a duration: %T", x)
	}
}

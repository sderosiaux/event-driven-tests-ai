package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	mrand "math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/data"
	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/event-driven-tests-ai/edt/pkg/httpc"
	"github.com/event-driven-tests-ai/edt/pkg/kafka"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
)

// Runner executes a scenario's steps and records observed events.
type Runner struct {
	Kafka     KafkaPort
	HTTP      HTTPPort
	Store     events.Store
	Generators map[string]*data.FakerGenerator // keyed by data alias from scenario.Spec.Data
	Logger    func(format string, args ...any) // optional, defaults to no-op
}

// New builds a Runner from the live connectors. Tests can construct a Runner
// directly with fakes implementing KafkaPort/HTTPPort.
func New(s *scenario.Scenario, kc KafkaPort, hc HTTPPort, store events.Store) *Runner {
	gens := make(map[string]*data.FakerGenerator, len(s.Spec.Data))
	reg := data.NewRegistry()
	for name, d := range s.Spec.Data {
		gens[name] = data.NewFakerGenerator(reg, d.Generator.Seed, d.Generator.Overrides)
	}
	return &Runner{
		Kafka:     kc,
		HTTP:      hc,
		Store:     store,
		Generators: gens,
		Logger:    func(string, ...any) {},
	}
}

// Run executes the scenario steps in declaration order.
func (r *Runner) Run(ctx context.Context, s *scenario.Scenario) error {
	for i := range s.Spec.Steps {
		step := &s.Spec.Steps[i]
		if err := r.runStep(ctx, step); err != nil {
			return fmt.Errorf("step %q: %w", step.Name, err)
		}
	}
	return nil
}

func (r *Runner) runStep(ctx context.Context, step *scenario.Step) error {
	switch {
	case step.Produce != nil:
		return r.runProduce(ctx, step)
	case step.Consume != nil:
		return r.runConsume(ctx, step)
	case step.HTTP != nil:
		return r.runHTTP(ctx, step)
	case step.Sleep != "":
		d, err := time.ParseDuration(step.Sleep)
		if err != nil {
			return fmt.Errorf("invalid sleep %q: %w", step.Sleep, err)
		}
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	default:
		return fmt.Errorf("step %q: no action declared", step.Name)
	}
}

// ---- produce ----------------------------------------------------------------

func (r *Runner) runProduce(ctx context.Context, step *scenario.Step) error {
	if r.Kafka == nil {
		return fmt.Errorf("produce: no kafka client configured")
	}
	p := step.Produce
	count := p.Count
	if count <= 0 {
		count = 1
	}

	// Rate limiter: parse "N/s" into a per-record sleep budget.
	tickEvery, err := parseRate(p.Rate)
	if err != nil {
		return err
	}

	// Deterministic step-scoped RNG for fail_rate decisions.
	// The seed derives from the step name so a given scenario+step always
	// yields the same failure sequence (important for reproducible CI runs).
	rng := mrand.New(mrand.NewSource(int64(fnv32(step.Name))))

	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		payload, err := r.resolvePayload(p.Payload)
		if err != nil {
			return err
		}
		valueBytes := encodeWireValue(payload)

		key := p.Key
		if key == "" {
			if m, ok := payload.(map[string]any); ok {
				if v, ok := m["orderId"]; ok {
					key = fmt.Sprint(v)
				}
			}
		}

		// Failure injection: skip the produce and record a failed event.
		if p.FailRate > 0 && rng.Float64() < p.FailRate {
			r.Store.Append(events.Event{
				Stream: p.Topic,
				Key:    key,
				Ts:     time.Now().UTC(),
				Payload: map[string]any{
					"injected_failure": nonEmptyOr(p.FailMode, "timeout"),
					"original":         payload,
				},
				Direction: events.ProducedFailed,
			})
		} else {
			rec := kafka.Record{Topic: p.Topic, Key: []byte(key), Value: valueBytes}
			out, err := r.Kafka.Produce(ctx, rec)
			direction := events.Produced
			if err != nil {
				direction = events.ProducedFailed
				r.Logger("produce error: %v", err)
			}
			r.Store.Append(events.Event{
				Stream:    p.Topic,
				Key:       string(out.Key),
				Ts:        time.Now().UTC(),
				Payload:   payload,
				Direction: direction,
			})
		}

		if tickEvery > 0 && i < count-1 {
			select {
			case <-time.After(tickEvery):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func nonEmptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ---- consume ----------------------------------------------------------------

// errMatched is a sentinel returned from the consume callback once a record
// satisfies the step's match rules; it lets us break out of PollFetches without
// being interpreted as a real error.
var errMatched = fmt.Errorf("__edt_match_satisfied__")

func (r *Runner) runConsume(ctx context.Context, step *scenario.Step) error {
	if r.Kafka == nil {
		return fmt.Errorf("consume: no kafka client configured")
	}
	c := step.Consume
	timeout := 30 * time.Second
	if c.Timeout != "" {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return fmt.Errorf("invalid timeout %q: %w", c.Timeout, err)
		}
		timeout = d
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Compile match rules once; nil matcher = "any record from the topic ends the step".
	m, err := compileMatcher(c.Match)
	if err != nil {
		return fmt.Errorf("match rules: %w", err)
	}

	var slow *scenario.SlowMode
	if c.SlowMode != nil && c.SlowMode.PauseEvery > 0 {
		slow = c.SlowMode
	}
	var pauseFor time.Duration
	if slow != nil {
		d, err := time.ParseDuration(slow.PauseFor)
		if err != nil {
			return fmt.Errorf("slow_mode.pause_for %q: %w", slow.PauseFor, err)
		}
		pauseFor = d
	}

	consumed := 0
	matched := false

	consumeErr := r.Kafka.Consume(cctx, kafka.ConsumeRequest{Topic: c.Topic, Group: c.Group}, func(rec kafka.Record) error {
		// Defensive: ignore records from any other topic the underlying
		// subscriber may have leaked (it should not, but cheap to guard).
		if rec.Topic != c.Topic {
			return nil
		}
		var payload any
		_ = json.Unmarshal(rec.Value, &payload)
		r.Store.Append(events.Event{
			Stream:    rec.Topic,
			Key:       string(rec.Key),
			Ts:        time.Unix(0, rec.Timestamp).UTC(),
			Payload:   payload,
			Direction: events.Consumed,
		})
		consumed++

		if m != nil {
			ok, err := m.matches(payload)
			if err != nil {
				return fmt.Errorf("match rule eval: %w", err)
			}
			if ok {
				matched = true
				return errMatched
			}
		} else {
			// No rules → first record is enough to advance the step.
			matched = true
			return errMatched
		}

		if slow != nil && consumed%slow.PauseEvery == 0 && pauseFor > 0 {
			select {
			case <-time.After(pauseFor):
			case <-cctx.Done():
				return cctx.Err()
			}
		}
		return nil
	})

	switch {
	case errors.Is(consumeErr, errMatched):
		return nil
	case matched:
		// Match happened but consume returned ctx error first — that's still a pass.
		return nil
	case errors.Is(consumeErr, context.DeadlineExceeded):
		return fmt.Errorf("consume timed out after %s with %d record(s) seen and no match", timeout, consumed)
	case errors.Is(consumeErr, context.Canceled):
		return consumeErr
	case consumeErr != nil:
		return consumeErr
	default:
		return nil
	}
}

// ---- http -------------------------------------------------------------------

func (r *Runner) runHTTP(ctx context.Context, step *scenario.Step) error {
	if r.HTTP == nil {
		return fmt.Errorf("http: no http client configured")
	}
	resp, err := r.HTTP.Do(ctx, step.HTTP)
	if err != nil {
		// Record a failure event but do not abort — checks decide pass/fail.
		r.Store.Append(events.Event{
			Stream:    "http:" + step.HTTP.Path,
			Ts:        time.Now().UTC(),
			Payload:   map[string]any{"error": err.Error()},
			Direction: events.HTTPCall,
		})
		return nil
	}
	r.Store.Append(events.Event{
		Stream:    "http:" + step.HTTP.Path,
		Ts:        time.Now().UTC(),
		Payload: map[string]any{
			"status":  resp.Status,
			"body":    resp.Body,
			"latency_ms": resp.Latency.Milliseconds(),
		},
		Direction: events.HTTPCall,
	})

	if step.HTTP.Expect != nil {
		if err := httpc.CheckExpectation(step.HTTP.Expect, resp); err != nil {
			return fmt.Errorf("http expect: %w", err)
		}
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------

// resolvePayload turns the produce.payload string into a value.
// "${data.<name>}" looks up the named generator and runs Generate().
// Anything that looks like a JSON object or array is parsed so the wire payload
// is not double-encoded later. Anything else is returned as a raw string.
func (r *Runner) resolvePayload(spec string) (any, error) {
	const prefix = "${data."
	if strings.HasPrefix(spec, prefix) && strings.HasSuffix(spec, "}") {
		name := strings.TrimSuffix(strings.TrimPrefix(spec, prefix), "}")
		gen, ok := r.Generators[name]
		if !ok {
			return nil, fmt.Errorf("unknown data alias %q", name)
		}
		return gen.Generate()
	}
	if looksLikeJSON(spec) {
		var v any
		if err := json.Unmarshal([]byte(spec), &v); err == nil {
			return v, nil
		}
		// Fallthrough: leave the raw string if it isn't valid JSON.
	}
	return spec, nil
}

func looksLikeJSON(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	switch t[0] {
	case '{', '[':
		return true
	default:
		return false
	}
}

// encodeWireValue serializes a payload for Kafka. Strings travel as raw bytes
// so plain text payloads do not gain extra JSON quotes; everything else is
// JSON-encoded (objects, arrays, numbers, bools).
func encodeWireValue(payload any) []byte {
	if s, ok := payload.(string); ok {
		return []byte(s)
	}
	b, _ := json.Marshal(payload)
	return b
}

// parseRate turns "50/s" into the per-record sleep duration. "" → 0 (no throttle).
// Supported units: /s, /ms.
func parseRate(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("rate %q must be N/<unit>", s)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("rate %q: invalid N", s)
	}
	unit := strings.TrimSpace(parts[1])
	switch unit {
	case "s":
		return time.Second / time.Duration(n), nil
	case "ms":
		return time.Millisecond / time.Duration(n), nil
	default:
		return 0, fmt.Errorf("rate %q: unsupported unit %q", s, unit)
	}
}

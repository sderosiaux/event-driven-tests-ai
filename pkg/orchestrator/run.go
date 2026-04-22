package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	mrand "math/rand"
	"strconv"
	"strings"
	"sync"
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
		valueBytes, _ := json.Marshal(payload)

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

	var wg sync.WaitGroup
	wg.Add(1)
	var consumeErr error
	consumed := 0
	go func() {
		defer wg.Done()
		err := r.Kafka.Consume(cctx, func(rec kafka.Record) error {
			var payload any
			_ = json.Unmarshal(rec.Value, &payload)
			r.Store.Append(events.Event{
				Stream:    rec.Topic,
				Key:       string(rec.Key),
				Ts:        time.Unix(0, rec.Timestamp),
				Payload:   payload,
				Direction: events.Consumed,
			})
			consumed++
			if slow != nil && consumed%slow.PauseEvery == 0 && pauseFor > 0 {
				select {
				case <-time.After(pauseFor):
				case <-cctx.Done():
					return cctx.Err()
				}
			}
			return nil
		})
		// Cancellation/timeout is expected; surface only "real" errors.
		if err != nil && !isContextErr(err) {
			consumeErr = err
		}
	}()
	wg.Wait()
	return consumeErr
}

func isContextErr(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
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
// Anything else is returned as a raw string.
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
	return spec, nil
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

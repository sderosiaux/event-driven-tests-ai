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

	"github.com/sderosiaux/event-driven-tests-ai/pkg/data"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/kafka"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// Runner executes a scenario's steps and records observed events.
type Runner struct {
	Kafka      KafkaPort
	HTTP       HTTPPort
	WebSocket  WebSocketPort                     // optional; required if any step.websocket present
	SSE        SSEPort                           // optional; required if any step.sse present
	GRPC       GRPCPort                          // optional; required if any step.grpc present
	Codec      CodecPort                         // optional Schema Registry codec
	Store      events.Store
	Generators map[string]*data.FakerGenerator // keyed by data alias from scenario.Spec.Data
	Logger     func(format string, args ...any) // optional, defaults to no-op
	RunID      string                            // injected as ${run.id} for interpolation

	interp *interpCtx
	active *scenario.Scenario // set by Run; used by step handlers to reach connector config
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
	if r.interp == nil {
		r.interp = newInterpCtx(r.RunID, time.Now().UTC().Format(time.RFC3339))
	}
	r.active = s
	defer func() { r.active = nil }()
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
	case step.WebSocket != nil:
		return r.runWebSocket(ctx, step)
	case step.SSE != nil:
		return r.runSSE(ctx, step)
	case step.GRPC != nil:
		return r.runGRPC(ctx, step)
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
	if err := validateProduceFailMode(p.FailMode); err != nil {
		return err
	}
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
		expandedPayload := r.interp.expand(p.Payload)
		payload, err := r.resolvePayload(expandedPayload)
		if err != nil {
			return err
		}
		valueBytes, err := r.encodeForWire(ctx, p.SchemaSubject, payload)
		if err != nil {
			return fmt.Errorf("produce %q: %w", p.Topic, err)
		}

		key := r.interp.expand(p.Key)
		if key == "" {
			if m, ok := payload.(map[string]any); ok {
				if v, ok := m["orderId"]; ok {
					key = fmt.Sprint(v)
				}
			}
		}

		// Failure injection: per fail_mode, either skip the produce, or
		// actually send a corrupted payload that downstream consumers will
		// reject. In both cases we record a ProducedFailed event so checks
		// can introspect.
		if p.FailRate > 0 && rng.Float64() < p.FailRate.Float64() {
			mode := nonEmptyOr(p.FailMode, "timeout")
			payloadOnWire := payload
			direction := events.ProducedFailed
			if mode == "schema_violation" {
				// Send mangled bytes — a half-truncated value is enough to
				// break Avro/Proto/JSON deserialization downstream.
				corrupted := corruptValue(valueBytes)
				rec := kafka.Record{Topic: p.Topic, Key: []byte(key), Value: corrupted}
				if _, err := r.Kafka.Produce(ctx, rec); err != nil {
					r.Logger("schema_violation produce error: %v", err)
				}
				payloadOnWire = map[string]any{"corrupted_bytes_len": len(corrupted)}
			}
			r.Store.Append(events.Event{
				Stream: p.Topic,
				Key:    key,
				Ts:     time.Now().UTC(),
				Payload: map[string]any{
					"injected_failure": mode,
					"original":         payloadOnWire,
				},
				Direction: direction,
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
			r.interp.recordPrevious(previousFromPayload(string(out.Key), payload))
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

// previousFromPayload flattens a step's emitted record into a map suitable
// for ${previous.X} substitution and CEL `previous` access. Top-level fields
// of the payload are merged in; the record key is exposed as `key`.
func previousFromPayload(key string, payload any) map[string]any {
	out := map[string]any{"key": key}
	if m, ok := payload.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	} else {
		out["payload"] = payload
	}
	return out
}

// corruptValue returns a deliberately damaged copy of v: the first half of the
// bytes, which is enough to break JSON/Avro/Protobuf decoders downstream.
func corruptValue(v []byte) []byte {
	if len(v) == 0 {
		return []byte{0xFF}
	}
	return append([]byte(nil), v[:len(v)/2]...)
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

	topic := r.interp.expand(c.Topic)
	group := r.interp.expand(c.Group)

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

	consumeErr := r.Kafka.Consume(cctx, kafka.ConsumeRequest{Topic: topic, Group: group}, func(rec kafka.Record) error {
		// Defensive: ignore records from any other topic the underlying
		// subscriber may have leaked (it should not, but cheap to guard).
		if rec.Topic != topic {
			return nil
		}
		payload := r.decodeForWire(ctx, rec.Value)
		r.Store.Append(events.Event{
			Stream:    rec.Topic,
			Key:       string(rec.Key),
			Ts:        time.Unix(0, rec.Timestamp).UTC(),
			Headers:   kafkaHeadersToStrings(rec.Headers),
			Payload:   payload,
			Direction: events.Consumed,
		})
		consumed++

		if m != nil {
			ok, err := m.matches(payload, r.interp.previous, r.interp.run)
			if err != nil {
				return fmt.Errorf("match rule eval: %w", err)
			}
			if ok {
				matched = true
				r.interp.recordPrevious(previousFromPayload(string(rec.Key), payload))
				return errMatched
			}
		} else {
			// No rules → first record is enough to advance the step.
			matched = true
			r.interp.recordPrevious(previousFromPayload(string(rec.Key), payload))
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
		return fmt.Errorf("consume timed out after %s with %d record(s) seen and no match for topic %q", timeout, consumed, topic)
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
	h := step.HTTP
	if err := validateHTTPFailMode(h.FailMode); err != nil {
		return err
	}
	// Apply ${run.X} / ${previous.X} interpolation to dynamic string fields.
	expanded := *h
	expanded.Path = r.interp.expand(h.Path)
	expanded.Body = r.interp.expand(h.Body)
	if len(h.Headers) > 0 {
		expanded.Headers = make(map[string]string, len(h.Headers))
		for k, v := range h.Headers {
			expanded.Headers[k] = r.interp.expand(v)
		}
	}
	h = &expanded

	// Deterministic per-step RNG so fail_rate sequences are reproducible.
	rng := mrand.New(mrand.NewSource(int64(fnv32(step.Name))))
	if h.FailRate > 0 && rng.Float64() < h.FailRate.Float64() {
		mode := nonEmptyOr(h.FailMode, "timeout")
		r.Store.Append(events.Event{
			Stream:    "http:" + h.Path,
			Ts:        time.Now().UTC(),
			Payload: map[string]any{
				"injected_failure": mode,
				"status":           injectedHTTPStatus(mode),
			},
			Direction: events.HTTPCall,
		})
		// Injected failures do not abort the scenario — checks decide.
		return nil
	}

	resp, err := r.HTTP.Do(ctx, h)
	if err != nil {
		r.Store.Append(events.Event{
			Stream:    "http:" + h.Path,
			Ts:        time.Now().UTC(),
			Payload:   map[string]any{"error": err.Error()},
			Direction: events.HTTPCall,
		})
		return nil
	}
	r.Store.Append(events.Event{
		Stream: "http:" + h.Path,
		Ts:     time.Now().UTC(),
		Payload: map[string]any{
			"status":     resp.Status,
			"body":       resp.Body,
			"latency_ms": resp.Latency.Milliseconds(),
		},
		Direction: events.HTTPCall,
	})

	if h.Expect != nil {
		if err := httpc.CheckExpectation(h.Expect, resp); err != nil {
			return fmt.Errorf("http expect: %w", err)
		}
	}
	r.interp.recordPrevious(map[string]any{
		"status": resp.Status,
		"body":   resp.Body,
	})
	return nil
}

// injectedHTTPStatus maps a fail_mode label to a synthetic HTTP status.
// Unknown modes default to 0 (no response).
func injectedHTTPStatus(mode string) int {
	switch mode {
	case "http_5xx", "5xx":
		return 503
	case "http_4xx", "4xx":
		return 429
	default:
		return 0 // timeout / unknown
	}
}

func validateProduceFailMode(mode string) error {
	switch mode {
	case "", "timeout", "broker_not_available", "schema_violation":
		return nil
	}
	return fmt.Errorf("produce.fail_mode %q not recognized (allowed: timeout, broker_not_available, schema_violation)", mode)
}

func validateHTTPFailMode(mode string) error {
	switch mode {
	case "", "timeout", "http_5xx", "5xx", "http_4xx", "4xx":
		return nil
	}
	return fmt.Errorf("http.fail_mode %q not recognized (allowed: timeout, http_5xx, http_4xx)", mode)
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

// encodeForWire dispatches between Schema Registry serialization and the raw
// JSON path based on whether the step names a schema subject and a codec is
// wired.
func (r *Runner) encodeForWire(ctx context.Context, subject string, payload any) ([]byte, error) {
	if subject != "" && r.Codec != nil {
		return r.Codec.Encode(ctx, subject, payload)
	}
	return encodeWireValue(payload), nil
}

func decodeConsumedValue(v []byte) any {
	var payload any
	if err := json.Unmarshal(v, &payload); err == nil {
		return payload
	}
	return string(v)
}

// decodeForWire picks between the Schema Registry codec and raw JSON based on
// the wire header: a record whose first byte is 0x00 is assumed to be an SR
// payload when a codec is configured. Failures fall back to JSON so producers
// that mix raw + SR on the same topic do not break the consumer.
func (r *Runner) decodeForWire(ctx context.Context, v []byte) any {
	if r.Codec != nil && len(v) >= 1 && v[0] == 0x00 {
		if decoded, err := r.Codec.Decode(ctx, v); err == nil {
			return decoded
		}
	}
	return decodeConsumedValue(v)
}

func kafkaHeadersToStrings(headers map[string][]byte) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = string(v)
	}
	return out
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

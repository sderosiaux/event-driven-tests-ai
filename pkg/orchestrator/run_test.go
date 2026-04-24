package orchestrator_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/kafka"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// ---- fakes -----------------------------------------------------------------

type fakeKafka struct {
	mu          sync.Mutex
	produced    []kafka.Record
	consumeRecs []kafka.Record // records the fake will deliver to Consume()
	lastReq     kafka.ConsumeRequest
}

func (f *fakeKafka) Produce(_ context.Context, r kafka.Record) (kafka.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.produced = append(f.produced, r)
	r.Partition = 0
	r.Offset = int64(len(f.produced) - 1)
	r.Timestamp = time.Now().UnixNano()
	return r, nil
}
func (f *fakeKafka) Consume(ctx context.Context, req kafka.ConsumeRequest, fn func(kafka.Record) error) error {
	f.mu.Lock()
	recs := append([]kafka.Record(nil), f.consumeRecs...)
	f.lastReq = req
	f.mu.Unlock()
	for _, r := range recs {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Honor topic filtering: only deliver records that belong to the
		// requested topic, mirroring real subscriber behaviour.
		if req.Topic != "" && r.Topic != req.Topic {
			continue
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeKafka) Close() {}

type fakeHTTP struct {
	next     *httpc.Response
	err      error
	lastStep *scenario.HTTPStep
}

func (f *fakeHTTP) Do(_ context.Context, s *scenario.HTTPStep) (*httpc.Response, error) {
	f.lastStep = s
	return f.next, f.err
}

// ---- tests -----------------------------------------------------------------

// Regression: codex P0 — inline JSON payloads must not be wrapped in JSON quotes.
func TestProduceInlineJSONIsNotDoubleEncoded(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{
		Spec: scenario.Spec{Steps: []scenario.Step{{
			Name:    "p",
			Produce: &scenario.ProduceStep{Topic: "orders", Payload: `{"orderId":"abc","amount":99.5}`, Count: 1},
		}}},
	}
	require.NoError(t, r.Run(context.Background(), s))
	require.Len(t, fk.produced, 1)
	value := string(fk.produced[0].Value)
	assert.Equal(t, `{"amount":99.5,"orderId":"abc"}`, value, "JSON object literal must travel as raw object bytes")
	assert.Equal(t, []byte("abc"), fk.produced[0].Key, "key derived from payload.orderId")
}

func TestProducePlainStringPayloadStaysString(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{
		Spec: scenario.Spec{Steps: []scenario.Step{{
			Name:    "p",
			Produce: &scenario.ProduceStep{Topic: "logs", Payload: `hello world`, Count: 1},
		}}},
	}
	require.NoError(t, r.Run(context.Background(), s))
	require.Len(t, fk.produced, 1)
	assert.Equal(t, []byte(`hello world`), fk.produced[0].Value, "plain string payload travels verbatim")
}

func TestProduceCount(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{
		Spec: scenario.Spec{Steps: []scenario.Step{{
			Name:    "p",
			Produce: &scenario.ProduceStep{Topic: "orders", Payload: `{"x":1}`, Count: 5},
		}}},
	}
	require.NoError(t, r.Run(context.Background(), s))
	assert.Len(t, fk.produced, 5)
	assert.Len(t, store.Query("orders"), 5)
}

func TestProduceWithGenerator(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}

	s := &scenario.Scenario{
		Spec: scenario.Spec{
			Data: map[string]scenario.Data{
				"orders": {Generator: scenario.Generator{
					Strategy: "faker", Seed: 42,
					Overrides: map[string]string{
						"orderId": "${uuid()}",
						"amount":  "${faker.number.float(10, 100)}",
					},
				}},
			},
			Steps: []scenario.Step{{
				Name:    "p",
				Produce: &scenario.ProduceStep{Topic: "orders", Payload: "${data.orders}", Count: 3},
			}},
		},
	}
	r := orchestrator.New(s, fk, nil, store)
	require.NoError(t, r.Run(context.Background(), s))

	require.Len(t, fk.produced, 3)
	for _, rec := range fk.produced {
		assert.NotEmpty(t, rec.Key, "orderId should populate the key")
		assert.NotEmpty(t, rec.Value)
	}
}

func TestProduceRateThrottles(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{
		Spec: scenario.Spec{Steps: []scenario.Step{{
			Name: "p",
			Produce: &scenario.ProduceStep{
				Topic: "t", Payload: `{"k":1}`,
				Count: 5, Rate: "10/s", // ~100ms between produces, ~400ms total
			},
		}}},
	}
	t0 := time.Now()
	require.NoError(t, r.Run(context.Background(), s))
	elapsed := time.Since(t0)
	assert.GreaterOrEqual(t, elapsed, 350*time.Millisecond, "5 records at 10/s must take ~400ms (4 gaps)")
}

func TestConsumeFirstRecordSatisfiesWhenNoMatchRules(t *testing.T) {
	now := time.Now()
	fk := &fakeKafka{
		consumeRecs: []kafka.Record{
			{Topic: "orders.ack", Key: []byte("1"), Value: []byte(`{"orderId":"1"}`), Timestamp: now.UnixNano()},
			{Topic: "orders.ack", Key: []byte("2"), Value: []byte(`{"orderId":"2"}`), Timestamp: now.UnixNano()},
		},
	}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)

	s := &scenario.Scenario{
		Spec: scenario.Spec{Steps: []scenario.Step{{
			Name:    "c",
			Consume: &scenario.ConsumeStep{Topic: "orders.ack", Group: "g", Timeout: "100ms"},
		}}},
	}
	require.NoError(t, r.Run(context.Background(), s))
	// No match rules → first record is enough to advance the step.
	got := store.Query("orders.ack")
	assert.Len(t, got, 1, "consume should stop after the first record when no match rules are defined")
	assert.Equal(t, "g", fk.lastReq.Group, "consume request should propagate the step's group")
}

// Regression: codex P0 — consume timeout with no match must surface as an error.
func TestConsumeTimeoutWithNoMatchIsError(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{} // delivers no records
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "wait",
		Consume: &scenario.ConsumeStep{Topic: "orders.ack", Timeout: "50ms"},
	}}}}
	err := r.Run(context.Background(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

// Codex S01/S02: match rules must see `previous` and `run` from earlier steps.
func TestConsumeMatchUsesPreviousFromProduce(t *testing.T) {
	now := time.Now()
	fk := &fakeKafka{
		consumeRecs: []kafka.Record{
			{Topic: "orders.ack", Key: []byte("nope"), Value: []byte(`{"orderId":"nope"}`), Timestamp: now.UnixNano()},
			{Topic: "orders.ack", Key: []byte("abc"), Value: []byte(`{"orderId":"abc"}`), Timestamp: now.UnixNano()},
			{Topic: "orders.ack", Key: []byte("zzz"), Value: []byte(`{"orderId":"zzz"}`), Timestamp: now.UnixNano()},
		},
	}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{
		{
			Name: "place",
			Produce: &scenario.ProduceStep{
				Topic: "orders", Payload: `{"orderId":"abc"}`, Count: 1,
			},
		},
		{
			Name: "wait",
			Consume: &scenario.ConsumeStep{
				Topic: "orders.ack", Timeout: "200ms",
				Match: []scenario.MatchRule{{Key: `payload.orderId == previous.orderId`}},
			},
		},
	}}}
	require.NoError(t, r.Run(context.Background(), s))
	got := store.Query("orders.ack")
	require.Len(t, got, 2, "should stop on the record matching previous.orderId == 'abc'")
}

// Codex S01/S02: ${previous.X} and ${run.id} substitute in topic, group, http.path, http.body.
func TestInterpolationInTopicGroupHTTPPath(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"path":"` + req.URL.Path + `"}`))
	})

	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	r := orchestrator.New(&scenario.Scenario{}, fk, httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL}), store)
	r.RunID = "r-test-9"

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{
		{
			Name: "p",
			Produce: &scenario.ProduceStep{
				Topic: "orders", Payload: `{"orderId":"abc"}`, Count: 1,
			},
		},
		{
			Name: "h",
			HTTP: &scenario.HTTPStep{
				Method: "GET",
				Path:   "/orders/${previous.orderId}/by/${run.id}",
			},
		},
	}}}
	require.NoError(t, r.Run(context.Background(), s))
	httpEvents := store.Query("http:/orders/${previous.orderId}/by/${run.id}")
	require.Empty(t, httpEvents, "stream key uses raw template (pre-interpolation)")
	httpEventsResolved := store.Query("http:/orders/abc/by/r-test-9")
	require.Len(t, httpEventsResolved, 1, "interpolated path should be the actual stream key")
	body := httpEventsResolved[0].Payload.(map[string]any)["body"].(map[string]any)
	assert.Equal(t, "/orders/abc/by/r-test-9", body["path"])
}

// Regression: codex P0 — match rules must filter and the rule must be evaluated against payload.
func TestConsumeMatchRuleSelectsTheRightRecord(t *testing.T) {
	now := time.Now()
	fk := &fakeKafka{
		consumeRecs: []kafka.Record{
			{Topic: "orders.ack", Key: []byte("1"), Value: []byte(`{"orderId":"1"}`), Timestamp: now.UnixNano()},
			{Topic: "orders.ack", Key: []byte("2"), Value: []byte(`{"orderId":"2"}`), Timestamp: now.UnixNano()},
			{Topic: "orders.ack", Key: []byte("3"), Value: []byte(`{"orderId":"3"}`), Timestamp: now.UnixNano()},
		},
	}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "c",
		Consume: &scenario.ConsumeStep{
			Topic:   "orders.ack",
			Timeout: "200ms",
			Match:   []scenario.MatchRule{{Key: `payload.orderId == "2"`}},
		},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	got := store.Query("orders.ack")
	assert.Len(t, got, 2, "should stop after the matching record (2 events read)")
}

// Regression: codex P0 — records from other topics must NOT leak into a step.
func TestConsumeIgnoresRecordsFromOtherTopics(t *testing.T) {
	now := time.Now()
	fk := &fakeKafka{
		consumeRecs: []kafka.Record{
			{Topic: "intruder", Key: []byte("x"), Value: []byte(`{}`), Timestamp: now.UnixNano()},
			{Topic: "orders.ack", Key: []byte("1"), Value: []byte(`{}`), Timestamp: now.UnixNano()},
		},
	}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "c",
		Consume: &scenario.ConsumeStep{Topic: "orders.ack", Timeout: "100ms"},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	assert.Empty(t, store.Query("intruder"))
	assert.Len(t, store.Query("orders.ack"), 1)
}

func TestConsumePreservesRawStringPayloadAndHeaders(t *testing.T) {
	now := time.Now()
	fk := &fakeKafka{
		consumeRecs: []kafka.Record{{
			Topic:     "logs",
			Key:       []byte("k1"),
			Value:     []byte("hello world"),
			Headers:   map[string][]byte{"trace-id": []byte("abc123")},
			Timestamp: now.UnixNano(),
		}},
	}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "c",
		Consume: &scenario.ConsumeStep{Topic: "logs", Timeout: "100ms"},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	got := store.Query("logs")
	require.Len(t, got, 1)
	assert.Equal(t, "hello world", got[0].Payload)
	assert.Equal(t, map[string]string{"trace-id": "abc123"}, got[0].Headers)
}

func TestConsumeWhitespaceOnlyMatchRuleFallsBackToFirstRecord(t *testing.T) {
	now := time.Now()
	fk := &fakeKafka{
		consumeRecs: []kafka.Record{
			{Topic: "orders.ack", Key: []byte("1"), Value: []byte(`{"orderId":"1"}`), Timestamp: now.UnixNano()},
			{Topic: "orders.ack", Key: []byte("2"), Value: []byte(`{"orderId":"2"}`), Timestamp: now.UnixNano()},
		},
	}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "c",
		Consume: &scenario.ConsumeStep{
			Topic:   "orders.ack",
			Timeout: "100ms",
			Match:   []scenario.MatchRule{{Key: "   "}},
		},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	assert.Len(t, store.Query("orders.ack"), 1)
}

func TestHTTPStepRecordsAndChecksExpect(t *testing.T) {
	fh := &fakeHTTP{next: &httpc.Response{Status: 200, Body: map[string]any{"ok": true}, Latency: 12 * time.Millisecond}}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, fh, store)

	s := &scenario.Scenario{
		Spec: scenario.Spec{Steps: []scenario.Step{{
			Name: "h",
			HTTP: &scenario.HTTPStep{
				Method: "GET", Path: "/health",
				Expect: &scenario.HTTPExpect{Status: 200},
			},
		}}},
	}
	require.NoError(t, r.Run(context.Background(), s))
	got := store.Query("http:/health")
	require.Len(t, got, 1)
}

func TestHTTPExpectFailureSurfaces(t *testing.T) {
	fh := &fakeHTTP{next: &httpc.Response{Status: 500}}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, nil, fh, store)

	s := &scenario.Scenario{
		Spec: scenario.Spec{Steps: []scenario.Step{{
			Name: "h",
			HTTP: &scenario.HTTPStep{Method: "GET", Path: "/x", Expect: &scenario.HTTPExpect{Status: 200}},
		}}},
	}
	err := r.Run(context.Background(), s)
	require.Error(t, err)
}

func TestSleepStep(t *testing.T) {
	r := orchestrator.New(&scenario.Scenario{}, nil, nil, events.NewMemStore(0))
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{Name: "s", Sleep: "50ms"}}}}
	t0 := time.Now()
	require.NoError(t, r.Run(context.Background(), s))
	assert.GreaterOrEqual(t, time.Since(t0), 50*time.Millisecond)
}

func TestProduceErrorIsRecordedAsFailedEvent(t *testing.T) {
	fk := &errKafka{err: errors.New("broker_not_available")}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "p",
		Produce: &scenario.ProduceStep{Topic: "t", Payload: `{"k":1}`, Count: 2},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	got := store.Query("t")
	require.Len(t, got, 2)
	for _, e := range got {
		assert.Equal(t, events.ProducedFailed, e.Direction)
	}
}

type errKafka struct{ err error }

func (e *errKafka) Produce(_ context.Context, r kafka.Record) (kafka.Record, error) { return r, e.err }
func (e *errKafka) Consume(ctx context.Context, _ kafka.ConsumeRequest, _ func(kafka.Record) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func (e *errKafka) Close() {}

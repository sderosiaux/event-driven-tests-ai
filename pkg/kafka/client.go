package kafka

import (
	"context"
	"fmt"
	"strings"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Client is the edt-facing wrapper around a *kgo.Client.
// One client per scenario run holds the long-lived producer; each call to
// Consume builds a fresh subscriber so per-step subscriptions are isolated.
type Client struct {
	cl       *kgo.Client // producer (long-lived)
	seeds    []string    // captured for building per-step consumers
	authOpts []kgo.Opt   // captured for building per-step consumers
}

// Record is the subset of kgo.Record edt callers interact with.
type Record struct {
	Topic     string
	Key       []byte
	Value     []byte
	Headers   map[string][]byte
	Partition int32
	Offset    int64
	Timestamp int64 // unix nanos when known
}

// NewClient builds a Client from a scenario.KafkaConnector.
// The returned Client holds a producer kgo.Client; consumers are built on
// demand by Consume() so each consume step has its own subscription.
func NewClient(c *scenario.KafkaConnector) (*Client, error) {
	if c == nil {
		return nil, fmt.Errorf("kafka: nil connector")
	}
	if strings.TrimSpace(c.BootstrapServers) == "" {
		return nil, fmt.Errorf("kafka: bootstrap_servers is required")
	}
	seeds := splitServers(c.BootstrapServers)

	authOpts, err := buildAuthOpts(c.Auth)
	if err != nil {
		return nil, err
	}

	prodOpts := append([]kgo.Opt{kgo.SeedBrokers(seeds...)}, authOpts...)
	cl, err := kgo.NewClient(prodOpts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: build producer: %w", err)
	}
	return &Client{cl: cl, seeds: seeds, authOpts: authOpts}, nil
}

// Close flushes any in-flight produces and releases connections.
func (c *Client) Close() {
	if c.cl != nil {
		c.cl.Close()
	}
}

// Ping verifies connectivity to the seed brokers — useful as a precheck
// before running a scenario to fail fast on bad credentials / unreachable cluster.
func (c *Client) Ping(ctx context.Context) error {
	return c.cl.Ping(ctx)
}

// Produce synchronously publishes one record. Returns the broker-assigned
// partition+offset. Callers who need batches can call Produce N times and
// rely on franz-go's internal batching.
func (c *Client) Produce(ctx context.Context, r Record) (Record, error) {
	kr := &kgo.Record{
		Topic: r.Topic,
		Key:   r.Key,
		Value: r.Value,
	}
	for k, v := range r.Headers {
		kr.Headers = append(kr.Headers, kgo.RecordHeader{Key: k, Value: v})
	}
	res := c.cl.ProduceSync(ctx, kr)
	if err := res.FirstErr(); err != nil {
		return r, fmt.Errorf("kafka: produce %s: %w", r.Topic, err)
	}
	// First (and only) produced record carries the assignment.
	rec, _ := res[0].Record, res[0].Err
	out := r
	out.Partition = rec.Partition
	out.Offset = rec.Offset
	out.Timestamp = rec.Timestamp.UnixNano()
	return out, nil
}

// ConsumeRequest narrows what a consume call needs.
type ConsumeRequest struct {
	Topic string // required
	Group string // optional; empty = direct (no group) consumption
}

// Consume builds a fresh subscriber for the given request and invokes fn for
// each fetched record until ctx is cancelled or fn returns an error.
// Fetch-level errors (broker down, auth failure, unknown topic) are surfaced
// as a wrapped error rather than degraded into "no records until timeout".
func (c *Client) Consume(ctx context.Context, req ConsumeRequest, fn func(Record) error) error {
	if req.Topic == "" {
		return fmt.Errorf("kafka: consume requires a topic")
	}
	opts := append([]kgo.Opt{kgo.SeedBrokers(c.seeds...)}, c.authOpts...)
	opts = append(opts, kgo.ConsumeTopics(req.Topic))
	if req.Group != "" {
		opts = append(opts, kgo.ConsumerGroup(req.Group))
	}
	sub, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("kafka: build subscriber: %w", err)
	}
	defer sub.Close()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fetches := sub.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return fmt.Errorf("kafka: subscriber closed mid-poll")
		}
		// Surface non-context fetch errors instead of swallowing them.
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fe := range errs {
				if fe.Err == context.Canceled || fe.Err == context.DeadlineExceeded {
					continue
				}
				return fmt.Errorf("kafka: fetch error topic=%s partition=%d: %w", fe.Topic, fe.Partition, fe.Err)
			}
		}
		iter := fetches.RecordIter()
		for !iter.Done() {
			kr := iter.Next()
			r := Record{
				Topic:     kr.Topic,
				Key:       kr.Key,
				Value:     kr.Value,
				Partition: kr.Partition,
				Offset:    kr.Offset,
				Timestamp: kr.Timestamp.UnixNano(),
				Headers:   make(map[string][]byte, len(kr.Headers)),
			}
			for _, h := range kr.Headers {
				r.Headers[h.Key] = h.Value
			}
			if err := fn(r); err != nil {
				return err
			}
		}
	}
}

func splitServers(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

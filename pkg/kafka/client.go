package kafka

import (
	"context"
	"fmt"
	"strings"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Client is the edt-facing wrapper around a *kgo.Client.
// One client per scenario run (producer + consumer capability).
type Client struct {
	cl *kgo.Client
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
// consumerGroup and consumeTopics are optional — pass "" / nil for producer-only.
func NewClient(c *scenario.KafkaConnector, consumerGroup string, consumeTopics []string) (*Client, error) {
	if c == nil {
		return nil, fmt.Errorf("kafka: nil connector")
	}
	if strings.TrimSpace(c.BootstrapServers) == "" {
		return nil, fmt.Errorf("kafka: bootstrap_servers is required")
	}
	seeds := splitServers(c.BootstrapServers)

	opts := []kgo.Opt{kgo.SeedBrokers(seeds...)}

	authOpts, err := buildAuthOpts(c.Auth)
	if err != nil {
		return nil, err
	}
	opts = append(opts, authOpts...)

	if consumerGroup != "" {
		opts = append(opts, kgo.ConsumerGroup(consumerGroup))
	}
	if len(consumeTopics) > 0 {
		opts = append(opts, kgo.ConsumeTopics(consumeTopics...))
	}

	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: build client: %w", err)
	}
	return &Client{cl: cl}, nil
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

// Consume calls fn for each record polled. It returns when ctx is cancelled or
// fn returns a non-nil error (which is propagated to the caller).
// Errors on individual fetches are logged but do not terminate the loop unless
// ctx has been cancelled.
func (c *Client) Consume(ctx context.Context, fn func(Record) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fetches := c.cl.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return fmt.Errorf("kafka: client closed mid-poll")
		}
		// Record-level errors continue; the caller decides via ctx cancellation.
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

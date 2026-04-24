package kafka

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Client is the edt-facing wrapper around a *kgo.Client.
// One client per scenario run holds the long-lived producer; consumers are
// cached per (group, topic) so multiple consume steps reusing the same group
// do not thrash the consumer-group coordinator with join/leave cycles.
type Client struct {
	cl       *kgo.Client // producer (long-lived)
	seeds    []string    // captured for building per-step consumers
	authOpts []kgo.Opt   // captured for building per-step consumers

	mu          sync.Mutex
	subscribers map[string]*kgo.Client // key = "group|topic"
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

	prodOpts := append([]kgo.Opt{
		kgo.SeedBrokers(seeds...),
		// Auto-create topics on first produce so demos and tests don't require
		// a pre-provisioned cluster. Without this, franz-go's metadata refresh
		// loop throttles per-record produces to ~one every MetadataMinAge
		// (~20s on defaults) when the topic doesn't yet exist.
		kgo.AllowAutoTopicCreation(),
	}, authOpts...)
	cl, err := kgo.NewClient(prodOpts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: build producer: %w", err)
	}
	return &Client{
		cl:          cl,
		seeds:       seeds,
		authOpts:    authOpts,
		subscribers: make(map[string]*kgo.Client),
	}, nil
}

// Close flushes any in-flight produces, commits any pending consumer offsets,
// leaves consumer groups cleanly, and releases all connections.
func (c *Client) Close() {
	c.mu.Lock()
	subs := c.subscribers
	c.subscribers = nil
	c.mu.Unlock()

	for key, sub := range subs {
		closeSubscriber(sub, key)
	}
	if c.cl != nil {
		c.cl.Close()
	}
}

// closeSubscriber commits in-memory offsets and leaves the group before
// closing the kgo.Client. A short bounded timeout protects against a hung
// coordinator. Errors are intentionally swallowed — Close is always best-effort.
func closeSubscriber(sub *kgo.Client, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Commit any uncommitted offsets so the next Consume reusing the same
	// group does not re-read the tail of the topic. Safe to call even on a
	// non-grouped subscriber (it's a no-op).
	if strings.Contains(key, "|") && !strings.HasPrefix(key, "|") {
		_ = sub.CommitUncommittedOffsets(ctx)
	}
	sub.Close()
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

// Consume invokes fn for each record fetched from the requested topic until
// ctx is cancelled or fn returns an error. Subscribers are cached by
// (group, topic), so repeated consume steps using the same group do not
// trigger a fresh join/sync/leave cycle each time. Fetch-level errors
// (broker down, auth failure, unknown topic) are surfaced as a wrapped error
// rather than degraded into "no records until timeout".
func (c *Client) Consume(ctx context.Context, req ConsumeRequest, fn func(Record) error) error {
	if req.Topic == "" {
		return fmt.Errorf("kafka: consume requires a topic")
	}
	sub, err := c.subscriberFor(req)
	if err != nil {
		return err
	}

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

// subscriberFor returns a (group, topic)-scoped subscriber, building it on
// first use and reusing it on subsequent calls.
func (c *Client) subscriberFor(req ConsumeRequest) (*kgo.Client, error) {
	key := req.Group + "|" + req.Topic
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscribers == nil {
		// Client was already Closed.
		return nil, fmt.Errorf("kafka: client is closed")
	}
	if sub, ok := c.subscribers[key]; ok {
		return sub, nil
	}
	opts := append([]kgo.Opt{kgo.SeedBrokers(c.seeds...)}, c.authOpts...)
	opts = append(opts, kgo.ConsumeTopics(req.Topic))
	if req.Group != "" {
		opts = append(opts, kgo.ConsumerGroup(req.Group))
	}
	sub, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: build subscriber: %w", err)
	}
	c.subscribers[key] = sub
	return sub, nil
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

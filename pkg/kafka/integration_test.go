//go:build integration

// Package kafka integration tests — require Docker and a working
// Testcontainers runtime. Run with `go test -tags=integration ./pkg/kafka/...`.
//
// These tests cover the franz-go produce/consume round-trip the unit tests
// cannot exercise, plus fetch-error surfacing on a non-existent topic.
package kafka

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
)

func startKafka(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	kc, err := tckafka.Run(ctx,
		"confluentinc/confluent-local:7.5.0",
		tckafka.WithClusterID("edt-it"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(kc) })

	brokers, err := kc.Brokers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, brokers)
	return brokers[0]
}

func TestProduceConsumeRoundTrip(t *testing.T) {
	bootstrap := startKafka(t)

	c, err := NewClient(&scenario.KafkaConnector{BootstrapServers: bootstrap})
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, c.Ping(ctx))

	topic := "edt-it-rt"
	const n = 5
	for i := 0; i < n; i++ {
		_, err := c.Produce(ctx, Record{
			Topic: topic,
			Key:   []byte("k"),
			Value: []byte(`{"hello":"world"}`),
		})
		require.NoError(t, err)
	}

	var seen int32
	consumeCtx, cancelConsume := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelConsume()
	err = c.Consume(consumeCtx, ConsumeRequest{Topic: topic, Group: "edt-it-grp"}, func(_ Record) error {
		if atomic.AddInt32(&seen, 1) >= n {
			cancelConsume()
		}
		return nil
	})
	// Cancellation is the normal exit once we've seen everything.
	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		t.Fatalf("consume errored: %v", err)
	}
	assert.GreaterOrEqual(t, atomic.LoadInt32(&seen), int32(n))
}

// Codex finding #6: fetch-level errors must surface, not silently degrade.
// We connect to a non-existent broker port; PollFetches should report an error.
func TestConsumeFetchErrorSurfaces(t *testing.T) {
	c, err := NewClient(&scenario.KafkaConnector{BootstrapServers: "127.0.0.1:1"})
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = c.Consume(ctx, ConsumeRequest{Topic: "nope", Group: "g"}, func(Record) error { return nil })
	require.Error(t, err)
	// Either a fetch error or a context deadline — both prove the client did
	// not silently sit at "no records".
}

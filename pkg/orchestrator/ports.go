// Package orchestrator runs a scenario's steps sequentially, feeding observed
// events into an events.Store so that checks can be evaluated afterward.
//
// All external systems (Kafka, HTTP) are accessed via narrow ports defined
// here so tests can supply fakes without spinning up real brokers.
package orchestrator

import (
	"context"

	"github.com/event-driven-tests-ai/edt/pkg/httpc"
	"github.com/event-driven-tests-ai/edt/pkg/kafka"
	"github.com/event-driven-tests-ai/edt/pkg/scenario"
)

// KafkaPort is the surface the orchestrator needs from a Kafka client.
//
// Consume takes a per-call ConsumeRequest so the orchestrator can drive
// step-scoped subscriptions (one topic + one group per consume step). This
// avoids the M1 antipattern where a single shared subscription leaked records
// across steps.
type KafkaPort interface {
	Produce(ctx context.Context, r kafka.Record) (kafka.Record, error)
	Consume(ctx context.Context, req kafka.ConsumeRequest, fn func(kafka.Record) error) error
	Close()
}

// ConsumeRequest is re-exported here for callers that prefer to import only
// the orchestrator package.
type ConsumeRequest = kafka.ConsumeRequest

// HTTPPort is the surface the orchestrator needs from an HTTP client.
type HTTPPort interface {
	Do(ctx context.Context, step *scenario.HTTPStep) (*httpc.Response, error)
}

// CodecPort is the surface the orchestrator needs from a Schema Registry
// codec: encode a Go value for a subject (produce) and decode a wire payload
// (consume). Nil is valid — scenarios without schema_registry use raw JSON.
type CodecPort interface {
	Encode(ctx context.Context, subject string, value any) ([]byte, error)
	Decode(ctx context.Context, data []byte) (any, error)
}

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
type KafkaPort interface {
	Produce(ctx context.Context, r kafka.Record) (kafka.Record, error)
	Consume(ctx context.Context, fn func(kafka.Record) error) error
	Close()
}

// HTTPPort is the surface the orchestrator needs from an HTTP client.
type HTTPPort interface {
	Do(ctx context.Context, step *scenario.HTTPStep) (*httpc.Response, error)
}

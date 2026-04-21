// Package events holds the in-memory event store used by the orchestrator
// and the check evaluator. Every produced, consumed, or HTTP-observed datum
// flows through here so that CEL checks can reason about them as streams.
package events

import "time"

// Direction marks the origin of an event from edt's point of view.
type Direction string

const (
	Produced       Direction = "produced"
	ProducedFailed Direction = "produced_failed"
	Consumed       Direction = "consumed"
	HTTPCall       Direction = "http"
	AgentOut       Direction = "agent_out"
)

// Event is the canonical unit recorded during a scenario run.
type Event struct {
	Stream    string            // logical stream name (topic, or synthetic key like "http:/path")
	Key       string            // correlation key (record key, orderId, etc.)
	Ts        time.Time         // event time (producer wall clock for producer events, broker ts when available for consumer)
	Headers   map[string]string // Kafka headers or HTTP headers
	Payload   any               // deserialized payload
	Direction Direction
}

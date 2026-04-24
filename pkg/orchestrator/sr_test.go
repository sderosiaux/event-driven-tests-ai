package orchestrator_test

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/kafka"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/orchestrator"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCodec is a deterministic Schema Registry codec used purely to prove the
// orchestrator dispatches encode/decode at the right boundaries. It prefixes
// payloads with 0x00 + big-endian 0x00000042 so the consumer path recognises
// the SR header and routes through Decode.
type stubCodec struct {
	encodeCalls []string
	decodeCalls int
}

func (c *stubCodec) Encode(_ context.Context, subject string, value any) ([]byte, error) {
	c.encodeCalls = append(c.encodeCalls, subject)
	body := []byte("stub:" + subject)
	header := []byte{0x00, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[1:], 0x42)
	return append(header, body...), nil
}

func (c *stubCodec) Decode(_ context.Context, data []byte) (any, error) {
	c.decodeCalls++
	if len(data) < 5 || data[0] != 0x00 {
		return nil, errors.New("stub: not an SR payload")
	}
	return map[string]any{"_stub_body": string(data[5:])}, nil
}

func TestProduceUsesCodecWhenSchemaSubjectSet(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	codec := &stubCodec{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	r.Codec = codec

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name: "p",
		Produce: &scenario.ProduceStep{
			Topic: "orders", Payload: `{"orderId":"abc"}`, Count: 2,
			SchemaSubject: "orders-value",
		},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))

	assert.Equal(t, []string{"orders-value", "orders-value"}, codec.encodeCalls)
	require.Len(t, fk.produced, 2)
	// Each record carries the stub header (0x00, 0, 0, 0, 0x42) + body.
	assert.Equal(t, byte(0x00), fk.produced[0].Value[0])
	assert.Equal(t, uint32(0x42), binary.BigEndian.Uint32(fk.produced[0].Value[1:5]))
	assert.Equal(t, "stub:orders-value", string(fk.produced[0].Value[5:]))
}

func TestProduceSkipsCodecWhenSubjectEmpty(t *testing.T) {
	store := events.NewMemStore(0)
	fk := &fakeKafka{}
	codec := &stubCodec{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	r.Codec = codec

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "p",
		Produce: &scenario.ProduceStep{Topic: "plain", Payload: `{"k":1}`, Count: 1},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))
	assert.Empty(t, codec.encodeCalls, "no subject → codec must not be consulted")
	assert.Equal(t, `{"k":1}`, string(fk.produced[0].Value))
}

func TestConsumeDecodesSRWireFormat(t *testing.T) {
	now := time.Now()
	header := []byte{0x00, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[1:], 0x42)
	body := append(header, []byte("stub:orders-value")...)

	fk := &fakeKafka{
		consumeRecs: []kafka.Record{
			{Topic: "orders.ack", Key: []byte("k"), Value: body, Timestamp: now.UnixNano()},
		},
	}
	store := events.NewMemStore(0)
	codec := &stubCodec{}
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	r.Codec = codec

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "c",
		Consume: &scenario.ConsumeStep{Topic: "orders.ack", Timeout: "50ms"},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))

	assert.Equal(t, 1, codec.decodeCalls)
	got := store.Query("orders.ack")
	require.Len(t, got, 1)
	m := got[0].Payload.(map[string]any)
	assert.Equal(t, "stub:orders-value", m["_stub_body"])
}

func TestConsumeFallsBackToJSONWhenNoMagicByte(t *testing.T) {
	fk := &fakeKafka{
		consumeRecs: []kafka.Record{
			{Topic: "plain", Key: []byte("k"), Value: []byte(`{"ok":true}`), Timestamp: time.Now().UnixNano()},
		},
	}
	store := events.NewMemStore(0)
	r := orchestrator.New(&scenario.Scenario{}, fk, nil, store)
	r.Codec = &stubCodec{}

	s := &scenario.Scenario{Spec: scenario.Spec{Steps: []scenario.Step{{
		Name:    "c",
		Consume: &scenario.ConsumeStep{Topic: "plain", Timeout: "50ms"},
	}}}}
	require.NoError(t, r.Run(context.Background(), s))

	got := store.Query("plain")
	require.Len(t, got, 1)
	m := got[0].Payload.(map[string]any)
	assert.Equal(t, true, m["ok"])
}

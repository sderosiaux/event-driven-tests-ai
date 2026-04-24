package schemaregistry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const protoOrder = `
syntax = "proto3";
package commerce;

message Order {
  string order_id = 1;
  string customer_id = 2;
  double amount = 3;
  repeated string tags = 4;
  Address shipping = 5;
  Status status = 6;

  message Address {
    string street = 1;
    string city = 2;
  }

  enum Status {
    PENDING = 0;
    PAID = 1;
    SHIPPED = 2;
  }
}
`

func TestProtoRoundTripHappyPath(t *testing.T) {
	h, err := newProtoHandler(protoOrder)
	require.NoError(t, err)

	in := map[string]any{
		"order_id":    "ord-1",
		"customer_id": "cust-9",
		"amount":      123.45,
		"tags":        []any{"priority", "vip"},
		"shipping": map[string]any{
			"street": "221b Baker St",
			"city":   "London",
		},
		"status": "PAID",
	}
	wire, err := h.encode(in)
	require.NoError(t, err)
	// First byte is the message-index shorthand (0x00), so the body is
	// everything after. Non-zero schema type + 0x00 prefix is the common case.
	assert.GreaterOrEqual(t, len(wire), 2, "wire form must at least carry the index byte + body")
	assert.Equal(t, byte(0x00), wire[0])

	out, err := h.decode(wire)
	require.NoError(t, err)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ord-1", m["order_id"])
	assert.Equal(t, "cust-9", m["customer_id"])
	assert.InDelta(t, 123.45, m["amount"], 1e-9)
	assert.Equal(t, "PAID", m["status"], "enums must round-trip as symbolic names")

	shipping := m["shipping"].(map[string]any)
	assert.Equal(t, "London", shipping["city"])

	tags := m["tags"].([]any)
	require.Len(t, tags, 2)
	assert.Equal(t, "priority", tags[0])
}

func TestProtoDropsUnknownFields(t *testing.T) {
	h, err := newProtoHandler(protoOrder)
	require.NoError(t, err)

	wire, err := h.encode(map[string]any{
		"order_id":         "x",
		"not_in_schema":    "ignored",
		"extra_metadata":   map[string]any{"k": "v"},
	})
	require.NoError(t, err, "unknown fields must not fail the whole encode")

	out, err := h.decode(wire)
	require.NoError(t, err)
	m := out.(map[string]any)
	assert.Equal(t, "x", m["order_id"])
	_, has := m["not_in_schema"]
	assert.False(t, has, "unknown fields must be dropped, not leaked through")
}

func TestProtoEmptyWireBytesRejected(t *testing.T) {
	h, err := newProtoHandler(protoOrder)
	require.NoError(t, err)
	_, err = h.decode(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty proto payload")
}

func TestProtoScalarTypeMismatchErrors(t *testing.T) {
	h, err := newProtoHandler(protoOrder)
	require.NoError(t, err)
	_, err = h.encode(map[string]any{"order_id": 42}) // int into string field
	require.Error(t, err)
	assert.Contains(t, err.Error(), `field "order_id"`)
}

func TestProtoUnknownEnumValueRejected(t *testing.T) {
	h, err := newProtoHandler(protoOrder)
	require.NoError(t, err)
	_, err = h.encode(map[string]any{"status": "LOST_IN_TRANSIT"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown enum value")
}

func TestProtoImportsRejectedWithClearMessage(t *testing.T) {
	withImport := `syntax = "proto3";
import "google/protobuf/timestamp.proto";
message X { google.protobuf.Timestamp ts = 1; }
`
	_, err := newProtoHandler(withImport)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "imports")
}

func TestProtoSchemaWithoutMessagesRejected(t *testing.T) {
	_, err := newProtoHandler(`syntax = "proto3"; package empty;`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no messages")
}

func TestProtoBuildHandlerDispatchesToProto(t *testing.T) {
	h, err := buildHandler(Schema{Type: TypeProtobuf, Schema: `syntax = "proto3"; message M { string s = 1; }`})
	require.NoError(t, err)
	require.NotNil(t, h)

	wire, err := h.encode(map[string]any{"s": "hi"})
	require.NoError(t, err)
	out, err := h.decode(wire)
	require.NoError(t, err)
	assert.Equal(t, "hi", out.(map[string]any)["s"])
}

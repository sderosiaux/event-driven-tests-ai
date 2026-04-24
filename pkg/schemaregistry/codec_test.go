package schemaregistry_test

import (
	"context"
	"encoding/binary"
	"net/http/httptest"
	"testing"

	sr "github.com/sderosiaux/event-driven-tests-ai/pkg/schemaregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const orderAvroSchema = `{
    "type": "record",
    "name": "Order",
    "fields": [
        {"name": "id",     "type": "string"},
        {"name": "amount", "type": "double"}
    ]
}`

const orderJSONSchema = `{
    "type": "object",
    "required": ["id", "amount"],
    "properties": {
        "id":     {"type": "string"},
        "amount": {"type": "number"}
    }
}`

func TestEncodeHeaderRoundTrip(t *testing.T) {
	h := sr.EncodeHeader(42)
	require.Len(t, h, sr.HeaderLen)
	assert.EqualValues(t, 0x00, h[0])
	assert.EqualValues(t, 42, binary.BigEndian.Uint32(h[1:]))

	id, body, err := sr.ParseHeader(append(h, 0xFF, 0xEE))
	require.NoError(t, err)
	assert.Equal(t, 42, id)
	assert.Equal(t, []byte{0xFF, 0xEE}, body)
}

func TestParseHeaderRejectsBadMagic(t *testing.T) {
	_, _, err := sr.ParseHeader([]byte{0x42, 0, 0, 0, 1})
	require.Error(t, err)
}

func TestParseHeaderRejectsShortPayload(t *testing.T) {
	_, _, err := sr.ParseHeader([]byte{0x00, 0, 0})
	require.Error(t, err)
}

func TestAvroEncodeDecodeViaCodec(t *testing.T) {
	reg := newFakeRegistry()
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	cli := sr.New(sr.Config{URL: srv.URL})
	ctx := context.Background()

	id, err := cli.RegisterSchema(ctx, "orders-value", orderAvroSchema, sr.TypeAvro)
	require.NoError(t, err)

	codec := sr.NewCodec(cli)
	value := map[string]any{"id": "ord-1", "amount": 99.5}
	wire, err := codec.Encode(ctx, "orders-value", value)
	require.NoError(t, err)

	gotID, _, _ := sr.ParseHeader(wire)
	assert.Equal(t, id, gotID, "wire header must reference the registered schema id")

	out, err := codec.Decode(ctx, wire)
	require.NoError(t, err)
	gotMap := out.(map[string]any)
	assert.Equal(t, "ord-1", gotMap["id"])
	assert.InDelta(t, 99.5, gotMap["amount"].(float64), 1e-9)
}

func TestJSONEncodeDecodeViaCodec(t *testing.T) {
	reg := newFakeRegistry()
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	cli := sr.New(sr.Config{URL: srv.URL})
	ctx := context.Background()
	_, err := cli.RegisterSchema(ctx, "orders-json", orderJSONSchema, sr.TypeJSON)
	require.NoError(t, err)

	codec := sr.NewCodec(cli)
	wire, err := codec.Encode(ctx, "orders-json", map[string]any{"id": "x", "amount": 12.0})
	require.NoError(t, err)

	out, err := codec.Decode(ctx, wire)
	require.NoError(t, err)
	gotMap := out.(map[string]any)
	assert.Equal(t, "x", gotMap["id"])
}

func TestJSONValidationRejectsMissingField(t *testing.T) {
	reg := newFakeRegistry()
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()
	cli := sr.New(sr.Config{URL: srv.URL})
	_, _ = cli.RegisterSchema(context.Background(), "orders-json", orderJSONSchema, sr.TypeJSON)

	codec := sr.NewCodec(cli)
	_, err := codec.Encode(context.Background(), "orders-json", map[string]any{"id": "x"}) // missing amount
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate")
}

func TestProtobufEncodeDecodeViaCodec(t *testing.T) {
	reg := newFakeRegistry()
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()
	cli := sr.New(sr.Config{URL: srv.URL})
	_, err := cli.RegisterSchema(context.Background(), "p", `syntax = "proto3";
message X { string id = 1; int32 n = 2; }`, sr.TypeProtobuf)
	require.NoError(t, err)

	codec := sr.NewCodec(cli)
	ctx := context.Background()
	wire, err := codec.Encode(ctx, "p", map[string]any{"id": "abc", "n": 42})
	require.NoError(t, err)
	// Schema-registry header + protobuf body round-trips through Decode.
	out, err := codec.Decode(ctx, wire)
	require.NoError(t, err)
	m := out.(map[string]any)
	assert.Equal(t, "abc", m["id"])
	assert.EqualValues(t, 42, m["n"])
}

func TestCodecCachesSchemaPerSubjectAndID(t *testing.T) {
	reg := newFakeRegistry()
	hits := 0
	wrapped := wrapCount(reg.handler(), &hits)
	srv := httptest.NewServer(wrapped)
	defer srv.Close()
	cli := sr.New(sr.Config{URL: srv.URL})
	ctx := context.Background()
	_, err := cli.RegisterSchema(ctx, "orders-value", orderAvroSchema, sr.TypeAvro)
	require.NoError(t, err)
	hits = 0

	codec := sr.NewCodec(cli)
	for i := 0; i < 5; i++ {
		_, err := codec.Encode(ctx, "orders-value", map[string]any{"id": "x", "amount": 1.0})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, hits, "codec must cache the subject lookup after first encode")
}

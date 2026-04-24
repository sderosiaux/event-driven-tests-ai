package schemaregistry

import (
	"testing"

	"github.com/hamba/avro/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex P1 #9: top-level non-record schemas must decode into their native
// shape, not fail because the handler forced a map target.

func TestAvroDecodeRecordStillYieldsMap(t *testing.T) {
	schema := `{"type":"record","name":"Order","fields":[{"name":"id","type":"string"}]}`
	h, err := newAvroHandler(schema)
	require.NoError(t, err)
	s, _ := avro.Parse(schema)
	wire, err := avro.Marshal(s, map[string]any{"id": "abc"})
	require.NoError(t, err)

	out, err := h.decode(wire)
	require.NoError(t, err)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "abc", m["id"])
}

func TestAvroDecodeTopLevelStringScalar(t *testing.T) {
	h, err := newAvroHandler(`{"type":"string"}`)
	require.NoError(t, err)
	s, _ := avro.Parse(`{"type":"string"}`)
	wire, err := avro.Marshal(s, "hello")
	require.NoError(t, err)

	out, err := h.decode(wire)
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
}

func TestAvroDecodeTopLevelArray(t *testing.T) {
	schema := `{"type":"array","items":"long"}`
	h, err := newAvroHandler(schema)
	require.NoError(t, err)
	s, _ := avro.Parse(schema)
	wire, err := avro.Marshal(s, []int64{1, 2, 3})
	require.NoError(t, err)

	out, err := h.decode(wire)
	require.NoError(t, err)
	arr, ok := out.([]any)
	require.True(t, ok)
	require.Len(t, arr, 3)
	assert.EqualValues(t, 1, arr[0])
}

func TestAvroDecodeTopLevelMap(t *testing.T) {
	schema := `{"type":"map","values":"string"}`
	h, err := newAvroHandler(schema)
	require.NoError(t, err)
	s, _ := avro.Parse(schema)
	wire, err := avro.Marshal(s, map[string]string{"k": "v"})
	require.NoError(t, err)

	out, err := h.decode(wire)
	require.NoError(t, err)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v", m["k"])
}

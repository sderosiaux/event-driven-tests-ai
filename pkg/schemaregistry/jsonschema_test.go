package schemaregistry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewJSONHandlerAcceptsStringWrappedSchema(t *testing.T) {
	h, err := newJSONHandler(`"{\"type\":\"object\",\"properties\":{\"id\":{\"type\":\"string\"}},\"required\":[\"id\"]}"`)
	require.NoError(t, err)

	_, err = h.encode(map[string]any{"id": "ok"})
	require.NoError(t, err)
}

func TestNewJSONHandlerRejectsMalformedSchemaText(t *testing.T) {
	_, err := newJSONHandler(`{not-json`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid json schema resource")
}

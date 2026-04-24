package schemaregistry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sr "github.com/event-driven-tests-ai/edt/pkg/schemaregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex P1 #7: latest-version cache must refresh after the configured TTL,
// otherwise a mid-run bump to schemaType/version is silently missed.
func TestCodecRefreshesLatestAfterTTL(t *testing.T) {
	reg := newFakeRegistry()
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()
	cli := sr.New(sr.Config{URL: srv.URL})

	_, err := cli.RegisterSchema(context.Background(), "t", orderAvroSchema, sr.TypeAvro)
	require.NoError(t, err)

	// Use a tiny TTL so the second Encode re-queries the registry.
	codec := sr.NewCodecWithTTL(cli, 10*time.Millisecond)

	wire1, err := codec.Encode(context.Background(), "t", map[string]any{"id": "a", "amount": 1.0})
	require.NoError(t, err)
	id1, _, _ := sr.ParseHeader(wire1)

	// Register a new version for the same subject — the codec should pick it
	// up once the TTL expires.
	_, err = cli.RegisterSchema(context.Background(), "t",
		`{"type":"record","name":"OrderV2","fields":[{"name":"id","type":"string"},{"name":"amount","type":"double"},{"name":"tag","type":["null","string"],"default":null}]}`,
		sr.TypeAvro)
	require.NoError(t, err)

	time.Sleep(25 * time.Millisecond)
	wire2, err := codec.Encode(context.Background(), "t", map[string]any{"id": "b", "amount": 2.0})
	require.NoError(t, err)
	id2, _, _ := sr.ParseHeader(wire2)

	assert.NotEqual(t, id1, id2, "codec must pick up the new schema id after TTL expires")
}

// Codex P1 #8: subjects containing reserved URL characters must still address
// the right registry entry. Without url.PathEscape, "orders/v1-value" would
// hit /subjects/orders/v1-value/versions/latest and 404.
func TestClientEscapesSubjectInURL(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":1,"subject":"x","version":1,"schema":"{}"}`))
	}))
	defer srv.Close()
	c := sr.New(sr.Config{URL: srv.URL})
	_, _ = c.GetLatestVersion(context.Background(), "orders/v1-value")

	want := "/subjects/" + url.PathEscape("orders/v1-value") + "/versions/latest"
	assert.Equal(t, want, seenPath)
	// Sanity: the raw slash inside the subject should be percent-encoded.
	assert.True(t, strings.Contains(seenPath, "%2F"), "subject slash should be percent-encoded, got %q", seenPath)
}

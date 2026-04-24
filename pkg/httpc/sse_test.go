package httpc_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseHandler writes a fixed sequence of SSE frames then leaves the connection
// open until the client disconnects — SSE streams normally stay alive, and
// we want to exercise the "stream ends cleanly on ctx cancel" path too.
func sseHandler(frames []string, hold time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		for _, frame := range frames {
			fmt.Fprint(w, frame)
			f.Flush()
		}
		if hold > 0 {
			select {
			case <-time.After(hold):
			case <-r.Context().Done():
			}
		}
	})
}

func TestSSERoundTripDecodesJSONData(t *testing.T) {
	srv := httptest.NewServer(sseHandler([]string{
		"event: order\ndata: {\"id\":1,\"n\":42}\n\n",
		"event: order\ndata: {\"id\":2,\"n\":43}\n\n",
	}, 0))
	defer srv.Close()

	client := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := client.StreamSSE(ctx, "/events", nil)
	require.NoError(t, err)
	defer stream.Close()

	ev, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, "order", ev.Name)
	data := ev.Data.(map[string]any)
	assert.EqualValues(t, 1, data["id"])

	ev, err = stream.Next(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 43, ev.Data.(map[string]any)["n"])
}

func TestSSENonJSONDataWrappedAsRaw(t *testing.T) {
	srv := httptest.NewServer(sseHandler([]string{
		"data: this is plain text\n\n",
	}, 0))
	defer srv.Close()

	client := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	stream, err := client.StreamSSE(context.Background(), "/stream", nil)
	require.NoError(t, err)
	defer stream.Close()

	ev, err := stream.Next(context.Background())
	require.NoError(t, err)
	m := ev.Data.(map[string]any)
	assert.Equal(t, "this is plain text", m["_raw"])
}

func TestSSEIgnoresKeepAliveCommentsAndIDLines(t *testing.T) {
	srv := httptest.NewServer(sseHandler([]string{
		": keep-alive\n\n",
		"id: abc\nevent: tick\ndata: {\"t\":1}\n\n",
	}, 0))
	defer srv.Close()

	client := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	stream, err := client.StreamSSE(context.Background(), "/stream", nil)
	require.NoError(t, err)
	defer stream.Close()

	ev, err := stream.Next(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "abc", ev.ID)
	assert.Equal(t, "tick", ev.Name)
}

func TestSSERejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	client := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	_, err := client.StreamSSE(context.Background(), "/fail", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "418")
}

func TestSSERejectsWrongContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"not":"sse"}`))
	}))
	defer srv.Close()
	client := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	_, err := client.StreamSSE(context.Background(), "/x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text/event-stream")
}

func TestSSEAdapterOpenReturnsSession(t *testing.T) {
	srv := httptest.NewServer(sseHandler([]string{"data: ok\n\n"}, 100*time.Millisecond))
	defer srv.Close()
	client := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	adapter := httpc.NewSSEAdapter(client)

	session, err := adapter.Open(context.Background(), &scenario.HTTPConnector{BaseURL: srv.URL}, &scenario.SSEStep{Path: "/x"})
	require.NoError(t, err)
	defer session.Close()

	ev, err := session.Next(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", ev.Data.(map[string]any)["_raw"])
}

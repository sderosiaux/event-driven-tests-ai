package ws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/ws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdapterRoundTripAgainstRealServer exercises the full pkg/ws stack end
// to end: httptest upgrade → Dial → SendJSON → server echo → Read → decoded
// JSON payload. Guards against wire-level regressions that a pure unit test
// with a fake session would miss.
func TestAdapterRoundTripAgainstRealServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		require.NoError(t, err)
		defer c.CloseNow()

		// Client's first frame → echo it, then close.
		var got map[string]any
		require.NoError(t, wsjson.Read(r.Context(), c, &got))
		require.NoError(t, wsjson.Write(r.Context(), c, map[string]any{"echo": got}))
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	adapter := ws.NewAdapter()
	session, err := adapter.Open(ctx, &scenario.WebSocketConnector{BaseURL: wsURL}, &scenario.WebSocketStep{Path: "/"})
	require.NoError(t, err)
	defer session.Close()

	require.NoError(t, session.SendJSON(ctx, map[string]any{"hello": "world"}))

	msg, err := session.Read(ctx)
	require.NoError(t, err)
	echo, ok := msg.Payload.(map[string]any)
	require.True(t, ok)
	inner, ok := echo["echo"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "world", inner["hello"])
}

// TestAdapterSendsBearerAuthHeader verifies that the Auth block on the
// connector is translated into an Authorization header on the upgrade
// request. The server echoes the header back as the first frame.
func TestAdapterSendsBearerAuthHeader(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		require.NoError(t, err)
		_ = c.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := ws.NewAdapter().Open(ctx, &scenario.WebSocketConnector{
		BaseURL: wsURL,
		Auth:    &scenario.HTTPAuth{Type: "bearer", Token: "sek"},
	}, &scenario.WebSocketStep{Path: "/"})
	require.NoError(t, err)
	session.Close()

	assert.Equal(t, "Bearer sek", seenAuth)
}

func TestDialRejectsMissingBaseURL(t *testing.T) {
	_, err := ws.Dial(context.Background(), ws.Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty base url")
}

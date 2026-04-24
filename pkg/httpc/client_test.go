package httpc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/httpc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestGETJSONBody(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "count": 42})
	})

	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	resp, err := c.Do(context.Background(), &scenario.HTTPStep{Method: "GET", Path: "/foo"})
	require.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
	body := resp.Body.(map[string]any)
	assert.Equal(t, "ok", body["status"])
}

func TestExpectStatusPasses(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	})
	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	resp, _ := c.Do(context.Background(), &scenario.HTTPStep{Method: "DELETE", Path: "/x"})

	assert.NoError(t, httpc.CheckExpectation(&scenario.HTTPExpect{Status: 204}, resp))
	assert.Error(t, httpc.CheckExpectation(&scenario.HTTPExpect{Status: 200}, resp))
}

func TestExpectBodyField(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"region":"eu","plan":"pro"}`))
	})
	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	resp, _ := c.Do(context.Background(), &scenario.HTTPStep{Path: "/me"})

	assert.NoError(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Status: 200,
		Body:   map[string]any{"region": "eu"},
	}, resp))

	assert.Error(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{"plan": "free"},
	}, resp))
}

func TestBearerAuth(t *testing.T) {
	gotAuth := ""
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	})
	c := httpc.NewClient(&scenario.HTTPConnector{
		BaseURL: srv.URL,
		Auth:    &scenario.HTTPAuth{Type: "bearer", Token: "tok-xyz"},
	})
	_, err := c.Do(context.Background(), &scenario.HTTPStep{Path: "/secure"})
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok-xyz", gotAuth)
}

func TestBasicAuth(t *testing.T) {
	gotUser, gotPass, gotOK := "", "", false
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		w.WriteHeader(200)
	})
	c := httpc.NewClient(&scenario.HTTPConnector{
		BaseURL: srv.URL,
		Auth:    &scenario.HTTPAuth{Type: "basic", User: "alice", Pass: "secret"},
	})
	_, err := c.Do(context.Background(), &scenario.HTTPStep{Path: "/x"})
	require.NoError(t, err)
	assert.True(t, gotOK)
	assert.Equal(t, "alice", gotUser)
	assert.Equal(t, "secret", gotPass)
}

// Codex finding: §6.2 documents matchers like `status: { in: [...] }`.
func TestExpectMatcherIn(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"picking"}`))
	})
	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	resp, _ := c.Do(context.Background(), &scenario.HTTPStep{Path: "/x"})

	assert.NoError(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{
			"status": map[string]any{"in": []any{"received", "picking", "shipped"}},
		},
	}, resp))

	assert.Error(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{
			"status": map[string]any{"in": []any{"cancelled"}},
		},
	}, resp))
}

func TestExpectMatcherRegex(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"order_42abc"}`))
	})
	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	resp, _ := c.Do(context.Background(), &scenario.HTTPStep{Path: "/x"})
	assert.NoError(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{"id": map[string]any{"regex": `^order_\d+[a-z]+$`}},
	}, resp))
}

// Codex N01: an HTTP body that legitimately is a single-key {in:[...]} object
// must be assertable via the $literal escape.
func TestExpectLiteralEscapesMatcherShape(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"filter":{"in":[1,2,3]}}`))
	})
	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	resp, _ := c.Do(context.Background(), &scenario.HTTPStep{Path: "/x"})

	// Without $literal this would be parsed as a matcher and fail because the
	// list has integers and the matcher would compare against a JSON value.
	assert.NoError(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{
			"filter": map[string]any{
				"$literal": map[string]any{"in": []any{1, 2, 3}},
			},
		},
	}, resp))
}

func TestExpectMatcherGtLte(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"count":42}`))
	})
	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	resp, _ := c.Do(context.Background(), &scenario.HTTPStep{Path: "/x"})
	assert.NoError(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{"count": map[string]any{"gt": 10}},
	}, resp))
	assert.NoError(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{"count": map[string]any{"lte": 42}},
	}, resp))
	assert.Error(t, httpc.CheckExpectation(&scenario.HTTPExpect{
		Body: map[string]any{"count": map[string]any{"gt": 100}},
	}, resp))
}

func TestPostBody(t *testing.T) {
	var got string
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		got = string(buf[:n])
		w.WriteHeader(201)
	})
	c := httpc.NewClient(&scenario.HTTPConnector{BaseURL: srv.URL})
	_, err := c.Do(context.Background(), &scenario.HTTPStep{
		Method: "POST",
		Path:   "/orders",
		Body:   `{"id":1}`,
	})
	require.NoError(t, err)
	assert.Equal(t, `{"id":1}`, got)
}

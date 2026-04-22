package schemaregistry_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sr "github.com/event-driven-tests-ai/edt/pkg/schemaregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRegistry is a minimal in-process Confluent-shaped Schema Registry.
type fakeRegistry struct {
	mu       sync.Mutex
	schemas  map[int]string                  // id → schema body
	types    map[int]sr.SchemaType            // id → type
	subjects map[string]map[int]int           // subject → version → id
	nextID   int
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		schemas:  make(map[int]string),
		types:    make(map[int]sr.SchemaType),
		subjects: make(map[string]map[int]int),
	}
}

func (f *fakeRegistry) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas/ids/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var id int
		if _, err := json.Marshal(r.URL.Path); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Parse "/schemas/ids/{id}" path manually.
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) != 4 {
			http.Error(w, "bad path", 400)
			return
		}
		if _, err := jsonNumberParse(parts[3], &id); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		body, ok := f.schemas[id]
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema":     body,
			"schemaType": f.types[id],
		})
	})
	mux.HandleFunc("/subjects/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// /subjects/{subject}/versions{|/latest}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/subjects/"), "/")
		if len(parts) < 2 {
			http.Error(w, "bad path", 400)
			return
		}
		subject := parts[0]
		switch r.Method {
		case http.MethodPost:
			var req struct {
				Schema     string        `json:"schema"`
				SchemaType sr.SchemaType `json:"schemaType"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			// Idempotent: dedupe by exact schema body.
			for id, body := range f.schemas {
				if body == req.Schema {
					_ = json.NewEncoder(w).Encode(map[string]int{"id": id})
					return
				}
			}
			f.nextID++
			id := f.nextID
			f.schemas[id] = req.Schema
			f.types[id] = req.SchemaType
			versions, ok := f.subjects[subject]
			if !ok {
				versions = make(map[int]int)
				f.subjects[subject] = versions
			}
			versions[len(versions)+1] = id
			_ = json.NewEncoder(w).Encode(map[string]int{"id": id})
		case http.MethodGet:
			versions, ok := f.subjects[subject]
			if !ok {
				http.Error(w, "subject not found", 404)
				return
			}
			latestVersion := 0
			for v := range versions {
				if v > latestVersion {
					latestVersion = v
				}
			}
			id := versions[latestVersion]
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         id,
				"subject":    subject,
				"version":    latestVersion,
				"schema":     f.schemas[id],
				"schemaType": f.types[id],
			})
		default:
			http.Error(w, "method not allowed", 405)
		}
	})
	return mux
}

// jsonNumberParse parses a string to int. Standalone to avoid strconv import noise.
func jsonNumberParse(s string, into *int) (int, error) {
	*into = 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, &json.SyntaxError{}
		}
		*into = (*into)*10 + int(s[i]-'0')
	}
	return *into, nil
}

func TestRegisterAndFetchByID(t *testing.T) {
	reg := newFakeRegistry()
	ts := httptest.NewServer(reg.handler())
	defer ts.Close()
	c := sr.New(sr.Config{URL: ts.URL})
	ctx := context.Background()

	id, err := c.RegisterSchema(ctx, "orders-value",
		`{"type":"record","name":"O","fields":[{"name":"id","type":"string"}]}`, sr.TypeAvro)
	require.NoError(t, err)
	assert.Greater(t, id, 0)

	got, err := c.GetSchemaByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, sr.TypeAvro, got.Type)
	assert.Contains(t, got.Schema, `"name":"O"`)
}

func TestRegisterIsIdempotent(t *testing.T) {
	reg := newFakeRegistry()
	ts := httptest.NewServer(reg.handler())
	defer ts.Close()
	c := sr.New(sr.Config{URL: ts.URL})
	ctx := context.Background()
	a, err := c.RegisterSchema(ctx, "s",
		`{"type":"record","name":"X","fields":[]}`, sr.TypeAvro)
	require.NoError(t, err)
	b, err := c.RegisterSchema(ctx, "s",
		`{"type":"record","name":"X","fields":[]}`, sr.TypeAvro)
	require.NoError(t, err)
	assert.Equal(t, a, b, "identical schema must keep its id")
}

func TestGetLatestVersion(t *testing.T) {
	reg := newFakeRegistry()
	ts := httptest.NewServer(reg.handler())
	defer ts.Close()
	c := sr.New(sr.Config{URL: ts.URL})
	ctx := context.Background()
	_, err := c.RegisterSchema(ctx, "s",
		`{"type":"record","name":"V1","fields":[]}`, sr.TypeAvro)
	require.NoError(t, err)
	id2, err := c.RegisterSchema(ctx, "s",
		`{"type":"record","name":"V2","fields":[]}`, sr.TypeAvro)
	require.NoError(t, err)
	got, err := c.GetLatestVersion(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, id2, got.ID)
	assert.Equal(t, 2, got.Version)
}

func TestBasicAuthHeaderSent(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	c := sr.New(sr.Config{URL: srv.URL, User: "u", Pass: "p"})
	_, err := c.ListSubjects(context.Background())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(seenAuth, "Basic "), "expected Basic auth, got %q", seenAuth)
}

func TestBearerOverridesBasic(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	c := sr.New(sr.Config{URL: srv.URL, User: "u", Pass: "p", BearerTk: "tok"})
	_, _ = c.ListSubjects(context.Background())
	assert.Equal(t, "Bearer tok", seenAuth)
}

func TestApicurioCCompatBasePath(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	c := sr.New(sr.Config{URL: srv.URL, BasePath: "/apis/ccompat/v6"})
	_, _ = c.ListSubjects(context.Background())
	assert.Equal(t, "/apis/ccompat/v6/subjects", seenPath)
}

func TestSchemaNotFoundSurfacesError(t *testing.T) {
	reg := newFakeRegistry()
	ts := httptest.NewServer(reg.handler())
	defer ts.Close()
	c := sr.New(sr.Config{URL: ts.URL})
	_, err := c.GetSchemaByID(context.Background(), 999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

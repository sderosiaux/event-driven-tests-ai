// Package schemaregistry talks to a Schema Registry HTTP endpoint and exposes
// pluggable Codec implementations (Avro, Protobuf, JSON Schema) that emit and
// parse the Confluent wire format ([0x00, schemaID(4 BE), payload]).
//
// The HTTP surface is the documented Confluent SR API which Apicurio also
// implements in its CC-compatibility mode. Native Apicurio v3 endpoints are
// reachable through the same client by setting BasePath = "/apis/ccompat/v6".
package schemaregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SchemaType enumerates the supported registry types. A subject's "schemaType"
// field on the wire defaults to AVRO; PROTOBUF and JSON are the other two.
type SchemaType string

const (
	TypeAvro     SchemaType = "AVRO"
	TypeProtobuf SchemaType = "PROTOBUF"
	TypeJSON     SchemaType = "JSON"
)

// Schema is the registry's view of a schema definition.
type Schema struct {
	ID      int        `json:"id"`
	Subject string     `json:"subject"`
	Version int        `json:"version"`
	Type    SchemaType `json:"schemaType,omitempty"`
	Schema  string     `json:"schema"`
}

// Client is the SR HTTP client. Safe for concurrent use; one client per
// scenario run is enough.
type Client struct {
	baseURL string
	auth    string // optional "Basic ..." or "Bearer ..." header
	http    *http.Client
}

// Config drives Client construction.
type Config struct {
	URL      string // e.g. http://sr:8081
	BasePath string // e.g. "" (Confluent), "/apis/ccompat/v6" (Apicurio native), "/api" (Apicurio compat)
	User     string // basic auth user (Confluent Cloud, Apicurio)
	Pass     string // basic auth pass
	BearerTk string // OAuth bearer token (Apicurio with Keycloak, Confluent Cloud OAuth)
}

// New builds a Client. Auth precedence: Bearer > Basic > none.
func New(cfg Config) *Client {
	url := strings.TrimRight(cfg.URL, "/") + cfg.BasePath
	c := &Client{
		baseURL: url,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
	switch {
	case cfg.BearerTk != "":
		c.auth = "Bearer " + cfg.BearerTk
	case cfg.User != "":
		c.auth = "Basic " + basicAuth(cfg.User, cfg.Pass)
	}
	return c
}

// GetSchemaByID fetches a schema by its registry id. Confluent SR exposes
// `GET /schemas/ids/{id}`; the response shape includes the schema text and a
// schemaType field (defaulting to AVRO when omitted).
func (c *Client) GetSchemaByID(ctx context.Context, id int) (Schema, error) {
	var raw struct {
		Schema     string     `json:"schema"`
		SchemaType SchemaType `json:"schemaType,omitempty"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/schemas/ids/%d", id), nil, &raw); err != nil {
		return Schema{}, err
	}
	if raw.SchemaType == "" {
		raw.SchemaType = TypeAvro
	}
	return Schema{ID: id, Schema: raw.Schema, Type: raw.SchemaType}, nil
}

// GetLatestVersion fetches the latest version of a subject. Used by producers
// that ask the registry for the schema id to embed in the wire header.
func (c *Client) GetLatestVersion(ctx context.Context, subject string) (Schema, error) {
	var raw struct {
		ID         int        `json:"id"`
		Subject    string     `json:"subject"`
		Version    int        `json:"version"`
		Schema     string     `json:"schema"`
		SchemaType SchemaType `json:"schemaType,omitempty"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/subjects/%s/versions/latest", subject), nil, &raw); err != nil {
		return Schema{}, err
	}
	if raw.SchemaType == "" {
		raw.SchemaType = TypeAvro
	}
	return Schema{ID: raw.ID, Subject: raw.Subject, Version: raw.Version, Schema: raw.Schema, Type: raw.SchemaType}, nil
}

// RegisterSchema uploads a schema for a subject and returns the registry id
// the broker will use for serialization. Idempotent: identical schemas keep
// their id.
func (c *Client) RegisterSchema(ctx context.Context, subject, schema string, t SchemaType) (int, error) {
	body, _ := json.Marshal(map[string]any{
		"schema":     schema,
		"schemaType": t,
	})
	var raw struct {
		ID int `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/subjects/%s/versions", subject), body, &raw); err != nil {
		return 0, err
	}
	return raw.ID, nil
}

// CheckCompatibility returns whether the candidate schema is compatible with
// the latest version of the subject. Useful when scenarios want to assert that
// a payload generator is still wire-compatible with a deployed schema.
func (c *Client) CheckCompatibility(ctx context.Context, subject, schema string, t SchemaType) (bool, error) {
	body, _ := json.Marshal(map[string]any{
		"schema":     schema,
		"schemaType": t,
	})
	var raw struct {
		IsCompatible bool `json:"is_compatible"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/compatibility/subjects/%s/versions/latest", subject), body, &raw); err != nil {
		return false, err
	}
	return raw.IsCompatible, nil
}

// ListSubjects returns every subject the registry knows about.
func (c *Client) ListSubjects(ctx context.Context) ([]string, error) {
	var subs []string
	if err := c.do(ctx, http.MethodGet, "/subjects", nil, &subs); err != nil {
		return nil, err
	}
	return subs, nil
}

// ---- HTTP plumbing ---------------------------------------------------------

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.schemaregistry.v1+json, application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	}
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("schemaregistry: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("schemaregistry: %s %s -> %d: %s", method, path, resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("schemaregistry: decode %s %s: %w", method, path, err)
	}
	return nil
}

// basicAuth duplicates net/http's internal helper to avoid importing that.
func basicAuth(user, pass string) string {
	credentials := user + ":" + pass
	return base64Encode([]byte(credentials))
}

// Package scenario defines the data model and YAML parser for edt scenarios.
//
// The canonical wire format is YAML. These types are also used to generate
// the JSON Schema that validates user-supplied scenarios.
package scenario

// Scenario is the top-level object a user authors.
type Scenario struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name   string            `yaml:"name" json:"name"`
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

type Spec struct {
	Connectors      Connectors       `yaml:"connectors" json:"connectors"`
	Data            map[string]Data  `yaml:"data,omitempty" json:"data,omitempty"`
	Steps           []Step           `yaml:"steps" json:"steps"`
	Checks          []Check          `yaml:"checks,omitempty" json:"checks,omitempty"`
	AgentUnderTest  *AgentUnderTest  `yaml:"agent_under_test,omitempty" json:"agent_under_test,omitempty"`
	Evals           []Eval           `yaml:"evals,omitempty" json:"evals,omitempty"`
}

// ---- Connectors -------------------------------------------------------------

type Connectors struct {
	Kafka     *KafkaConnector     `yaml:"kafka,omitempty" json:"kafka,omitempty"`
	HTTP      *HTTPConnector      `yaml:"http,omitempty" json:"http,omitempty"`
	WebSocket *WebSocketConnector `yaml:"websocket,omitempty" json:"websocket,omitempty"`
	GRPC      *GRPCConnector      `yaml:"grpc,omitempty" json:"grpc,omitempty"`
}

// GRPCConnector points at one gRPC server. TLS defaults to off (plaintext);
// Auth is a bearer token added as `authorization: Bearer <token>` metadata
// on every RPC — the most common auth pattern for gateway-fronted services.
type GRPCConnector struct {
	Address string         `yaml:"address" json:"address"`
	TLS     bool           `yaml:"tls,omitempty" json:"tls,omitempty"`
	Auth    *GRPCAuth      `yaml:"auth,omitempty" json:"auth,omitempty"`
}

type GRPCAuth struct {
	Type  string `yaml:"type" json:"type"` // bearer (M6); more later
	Token string `yaml:"token,omitempty" json:"token,omitempty"`
}

// WebSocketConnector wires a scenario to a WebSocket endpoint. BaseURL must be
// ws:// or wss://; Path is joined per-step. Auth reuses HTTPAuth because the
// WebSocket handshake is HTTP and most servers share auth with their REST API.
type WebSocketConnector struct {
	BaseURL string    `yaml:"base_url" json:"base_url"`
	Auth    *HTTPAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

type KafkaConnector struct {
	BootstrapServers string                `yaml:"bootstrap_servers" json:"bootstrap_servers"`
	Auth             *KafkaAuth            `yaml:"auth,omitempty" json:"auth,omitempty"`
	SchemaRegistry   *SchemaRegistryConfig `yaml:"schema_registry,omitempty" json:"schema_registry,omitempty"`
}

// KafkaAuthType enumerates supported Kafka auth mechanisms in M1/M3.
type KafkaAuthType string

const (
	KafkaAuthSASLPlain    KafkaAuthType = "sasl_plain"
	KafkaAuthSASLScram256 KafkaAuthType = "sasl_scram_sha_256"
	KafkaAuthSASLScram512 KafkaAuthType = "sasl_scram_sha_512"
	KafkaAuthMTLS         KafkaAuthType = "mtls"
	KafkaAuthOAuthBearer  KafkaAuthType = "sasl_oauthbearer"
	KafkaAuthAWSIAM       KafkaAuthType = "aws_iam"
)

type KafkaAuth struct {
	Type     KafkaAuthType `yaml:"type" json:"type"`
	Username string        `yaml:"username,omitempty" json:"username,omitempty"`
	Password string        `yaml:"password,omitempty" json:"password,omitempty"`
	// mTLS
	CAFile   string `yaml:"ca_file,omitempty" json:"ca_file,omitempty"`
	CertFile string `yaml:"cert_file,omitempty" json:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty" json:"key_file,omitempty"`
	// OAuth / IAM (M3)
	TokenURL string `yaml:"token_url,omitempty" json:"token_url,omitempty"`
	ClientID string `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	Scopes   []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	Region   string `yaml:"region,omitempty" json:"region,omitempty"`
}

type SchemaRegistryConfig struct {
	URL      string `yaml:"url" json:"url"`
	Flavor   string `yaml:"flavor,omitempty" json:"flavor,omitempty"` // confluent | apicurio
	BasePath string `yaml:"base_path,omitempty" json:"base_path,omitempty"`
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
	BearerTk string `yaml:"bearer_token,omitempty" json:"bearer_token,omitempty"`
}

type HTTPConnector struct {
	BaseURL string    `yaml:"base_url" json:"base_url"`
	Auth    *HTTPAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

type HTTPAuth struct {
	Type  string `yaml:"type" json:"type"` // bearer | basic | none
	Token string `yaml:"token,omitempty" json:"token,omitempty"`
	User  string `yaml:"user,omitempty" json:"user,omitempty"`
	Pass  string `yaml:"pass,omitempty" json:"pass,omitempty"`
}

// ---- Data -------------------------------------------------------------------

type Data struct {
	Schema    *DataSchema `yaml:"schema,omitempty" json:"schema,omitempty"`
	Generator Generator   `yaml:"generator" json:"generator"`
}

type DataSchema struct {
	Source  string `yaml:"source" json:"source"` // schema_registry | inline | file
	Subject string `yaml:"subject,omitempty" json:"subject,omitempty"`
	Inline  string `yaml:"inline,omitempty" json:"inline,omitempty"`
	File    string `yaml:"file,omitempty" json:"file,omitempty"`
}

type Generator struct {
	Strategy  string            `yaml:"strategy" json:"strategy"` // faker
	Seed      int64             `yaml:"seed,omitempty" json:"seed,omitempty"`
	Overrides map[string]string `yaml:"overrides,omitempty" json:"overrides,omitempty"`
}

// ---- Steps ------------------------------------------------------------------

type Step struct {
	Name      string             `yaml:"name" json:"name"`
	Produce   *ProduceStep       `yaml:"produce,omitempty" json:"produce,omitempty"`
	Consume   *ConsumeStep       `yaml:"consume,omitempty" json:"consume,omitempty"`
	HTTP      *HTTPStep          `yaml:"http,omitempty" json:"http,omitempty"`
	WebSocket *WebSocketStep     `yaml:"websocket,omitempty" json:"websocket,omitempty"`
	SSE       *SSEStep           `yaml:"sse,omitempty" json:"sse,omitempty"`
	GRPC      *GRPCStep          `yaml:"grpc,omitempty" json:"grpc,omitempty"`
	Sleep     string             `yaml:"sleep,omitempty" json:"sleep,omitempty"`
}

// GRPCStep drives a single unary RPC against connectors.grpc.
//
// The .proto source is supplied inline (Proto) or via file (ProtoFile).
// Method is the fully-qualified name "package.Service/Method". Request is a
// JSON document that is decoded into a dynamicpb.Message for the method's
// input type; Expect compares the response to a caller-supplied shape
// (similar to HTTPStep.Expect). Streaming RPCs are out of scope for M6-T2
// and error clearly.
type GRPCStep struct {
	Proto     string          `yaml:"proto,omitempty" json:"proto,omitempty"`
	ProtoFile string          `yaml:"proto_file,omitempty" json:"proto_file,omitempty"`
	Method    string          `yaml:"method" json:"method"`
	Request   string          `yaml:"request" json:"request"`
	Expect    *GRPCExpect     `yaml:"expect,omitempty" json:"expect,omitempty"`
}

type GRPCExpect struct {
	Code int                    `yaml:"code,omitempty" json:"code,omitempty"` // gRPC status code; 0 = OK
	Body map[string]any         `yaml:"body,omitempty" json:"body,omitempty"`
}

// SSEStep subscribes to an HTTP Server-Sent Events stream and records each
// event into the events store under stream name "sse:<path>". Uses the
// existing HTTP connector (SSE is just HTTP with Accept: text/event-stream).
// Semantics mirror ConsumeStep: Count bound, Timeout, CEL Match rules.
type SSEStep struct {
	Path     string       `yaml:"path" json:"path"`
	Count    int          `yaml:"count,omitempty" json:"count,omitempty"` // 0 = wait for first match or timeout
	Timeout  string       `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Match    []MatchRule  `yaml:"match,omitempty" json:"match,omitempty"`
	SlowMode *SlowMode    `yaml:"slow_mode,omitempty" json:"slow_mode,omitempty"`
}

// WebSocketStep dials the connector, optionally sends a JSON message, then
// reads up to Count messages (or until Timeout) and persists each into the
// events store under stream name "ws:<path>". Match rules work the same as
// ConsumeStep — evaluated against each received payload until one matches.
// SlowMode is honoured per inbound message, not per send.
type WebSocketStep struct {
	Path     string       `yaml:"path" json:"path"`
	Send     string       `yaml:"send,omitempty" json:"send,omitempty"` // optional initial JSON frame, goes through ${interp}
	Count    int          `yaml:"count,omitempty" json:"count,omitempty"` // 0 = wait for first match or timeout
	Timeout  string       `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Match    []MatchRule  `yaml:"match,omitempty" json:"match,omitempty"`
	SlowMode *SlowMode    `yaml:"slow_mode,omitempty" json:"slow_mode,omitempty"`
}

type ProduceStep struct {
	Topic         string     `yaml:"topic" json:"topic"`
	Payload       string     `yaml:"payload" json:"payload"` // ref to data key, e.g. ${data.orders}
	Key           string     `yaml:"key,omitempty" json:"key,omitempty"`
	Rate          string     `yaml:"rate,omitempty" json:"rate,omitempty"` // e.g. "50/s"
	Count         int        `yaml:"count,omitempty" json:"count,omitempty"`
	FailRate      Percentage `yaml:"fail_rate,omitempty" json:"fail_rate,omitempty"`
	FailMode      string     `yaml:"fail_mode,omitempty" json:"fail_mode,omitempty"` // schema_violation | timeout | broker_not_available
	SchemaSubject string     `yaml:"schema_subject,omitempty" json:"schema_subject,omitempty"`
}

type ConsumeStep struct {
	Topic    string       `yaml:"topic" json:"topic"`
	Group    string       `yaml:"group,omitempty" json:"group,omitempty"`
	Timeout  string       `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Match    []MatchRule  `yaml:"match,omitempty" json:"match,omitempty"`
	SlowMode *SlowMode    `yaml:"slow_mode,omitempty" json:"slow_mode,omitempty"`
}

type MatchRule struct {
	Key string `yaml:"key,omitempty" json:"key,omitempty"` // CEL expression
}

type SlowMode struct {
	PauseEvery int    `yaml:"pause_every" json:"pause_every"`
	PauseFor   string `yaml:"pause_for" json:"pause_for"`
}

type HTTPStep struct {
	Method   string            `yaml:"method" json:"method"`
	Path     string            `yaml:"path" json:"path"`
	Headers  map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body     string            `yaml:"body,omitempty" json:"body,omitempty"`
	Expect   *HTTPExpect       `yaml:"expect,omitempty" json:"expect,omitempty"`
	FailRate Percentage        `yaml:"fail_rate,omitempty" json:"fail_rate,omitempty"`
	FailMode string            `yaml:"fail_mode,omitempty" json:"fail_mode,omitempty"` // timeout | http_5xx
}

type HTTPExpect struct {
	Status  int                    `yaml:"status,omitempty" json:"status,omitempty"`
	Headers map[string]string      `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body    map[string]any         `yaml:"body,omitempty" json:"body,omitempty"`
}

// ---- Checks -----------------------------------------------------------------

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

type Check struct {
	Name     string   `yaml:"name" json:"name"`
	Expr     string   `yaml:"expr" json:"expr"`
	Window   string   `yaml:"window,omitempty" json:"window,omitempty"`
	Severity Severity `yaml:"severity,omitempty" json:"severity,omitempty"`
}

// ---- Agent-under-test (eval mode, M5) --------------------------------------

type AgentUnderTest struct {
	Name          string   `yaml:"name" json:"name"`
	Consumes      []string `yaml:"consumes,omitempty" json:"consumes,omitempty"`
	Produces      []string `yaml:"produces,omitempty" json:"produces,omitempty"`
	HTTPEndpoints []string `yaml:"http_endpoints,omitempty" json:"http_endpoints,omitempty"`
}

type Eval struct {
	Name      string        `yaml:"name" json:"name"`
	Expr      string        `yaml:"expr,omitempty" json:"expr,omitempty"`
	Judge     *Judge        `yaml:"judge,omitempty" json:"judge,omitempty"`
	Threshold *EvalThreshold `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Severity  Severity      `yaml:"severity,omitempty" json:"severity,omitempty"`
}

type Judge struct {
	Model         string `yaml:"model" json:"model"`
	Rubric        string `yaml:"rubric" json:"rubric"`
	RubricVersion string `yaml:"rubric_version,omitempty" json:"rubric_version,omitempty"`
}

type EvalThreshold struct {
	Aggregate string `yaml:"aggregate,omitempty" json:"aggregate,omitempty"` // avg | p50 | p95 | min | max
	Value     string `yaml:"value" json:"value"`                              // CEL-like: ">= 4.2"
	Over      string `yaml:"over,omitempty" json:"over,omitempty"`            // "50 runs" | "1h"
}

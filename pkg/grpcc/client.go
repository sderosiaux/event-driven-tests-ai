// Package grpcc drives unary gRPC calls from a scenario step without
// compile-time .proto stubs.
//
// Authors ship the service .proto inline (or via file); we compile it with
// bufbuild/protocompile, look up the method descriptor, marshal the JSON
// request into a dynamicpb.Message, invoke the RPC, and decode the response
// back to map[string]any for CEL match/Expect.
//
// Named grpcc so the package import does not shadow google.golang.org/grpc.
package grpcc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"crypto/tls"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
)

// Response is the normalized result of a unary RPC.
type Response struct {
	Code    int            `json:"code"`
	Message string         `json:"message,omitempty"` // status error message when Code != 0
	Body    map[string]any `json:"body,omitempty"`
	Latency time.Duration  `json:"latency"`
}

// Client wraps a grpc.ClientConn and a per-scenario descriptor cache.
type Client struct {
	conn *grpc.ClientConn
	auth *scenario.GRPCAuth
}

// Dial establishes a client connection using the connector's auth + TLS
// settings. Caller must Close.
func Dial(ctx context.Context, conn *scenario.GRPCConnector) (*Client, error) {
	if conn == nil || conn.Address == "" {
		return nil, fmt.Errorf("grpc: connector address is required")
	}
	var creds credentials.TransportCredentials
	if conn.TLS {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	} else {
		creds = insecure.NewCredentials()
	}
	cc, err := grpc.NewClient(conn.Address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("grpc: dial %s: %w", conn.Address, err)
	}
	return &Client{conn: cc, auth: conn.Auth}, nil
}

// Close releases the underlying grpc.ClientConn.
func (c *Client) Close() { _ = c.conn.Close() }

// Invoke runs a unary RPC. Returns a Response even on non-OK status so the
// orchestrator can record the code+message as a failed-call event without
// turning gRPC errors into Go errors at the wrong layer.
func (c *Client) Invoke(ctx context.Context, step *scenario.GRPCStep) (*Response, error) {
	inputMD, outputMD, err := resolveMethod(step)
	if err != nil {
		return nil, err
	}
	req := dynamicpb.NewMessage(inputMD)
	if step.Request != "" {
		if err := protojson.Unmarshal([]byte(step.Request), req); err != nil {
			return nil, fmt.Errorf("grpc: request body: %w", err)
		}
	}
	resp := dynamicpb.NewMessage(outputMD)

	ctx = c.appendAuth(ctx)

	path, err := invokePath(step.Method)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	err = c.conn.Invoke(ctx, path, req, resp)
	latency := time.Since(start)

	out := &Response{Latency: latency}
	if err != nil {
		st, _ := status.FromError(err)
		out.Code = int(st.Code())
		out.Message = st.Message()
		return out, nil
	}
	out.Body, err = messageToMap(resp)
	if err != nil {
		return nil, fmt.Errorf("grpc: decode response: %w", err)
	}
	return out, nil
}

func (c *Client) appendAuth(ctx context.Context) context.Context {
	if c.auth == nil {
		return ctx
	}
	switch c.auth.Type {
	case "bearer":
		return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.auth.Token)
	}
	return ctx
}

// invokePath normalises a Method like "helloworld.Greeter/SayHello" or the
// gRPC wire path "/helloworld.Greeter/SayHello" to the wire form.
func invokePath(method string) (string, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		return "", fmt.Errorf("grpc: method is required")
	}
	if !strings.HasPrefix(method, "/") {
		method = "/" + method
	}
	if strings.Count(method, "/") != 2 {
		return "", fmt.Errorf("grpc: method %q must be of the form 'package.Service/Method'", method)
	}
	return method, nil
}

// resolveMethod compiles the step's .proto (inline or file) and returns the
// input/output message descriptors for the requested method. The compile is
// not cached between calls; scenarios with many RPCs can take the tiny hit
// in exchange for fresh compilation and a simpler cache story.
func resolveMethod(step *scenario.GRPCStep) (input, output protoreflect.MessageDescriptor, err error) {
	source := step.Proto
	if source == "" && step.ProtoFile != "" {
		b, readErr := os.ReadFile(step.ProtoFile)
		if readErr != nil {
			return nil, nil, fmt.Errorf("grpc: read proto_file: %w", readErr)
		}
		source = string(b)
	}
	if source == "" {
		return nil, nil, fmt.Errorf("grpc: step must set either `proto` (inline) or `proto_file`")
	}
	if strings.Contains(source, "\nimport ") || strings.HasPrefix(strings.TrimSpace(source), "import ") {
		return nil, nil, fmt.Errorf("grpc: .proto with imports is not yet supported — inline the dependency")
	}
	path := "service.proto"
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{path: source}),
		}),
	}
	files, err := compiler.Compile(context.Background(), path)
	if err != nil {
		return nil, nil, fmt.Errorf("grpc: compile proto: %w", err)
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("grpc: proto compile produced no descriptors")
	}
	file := files[0]

	// Method format: package.Service/Method (leading slash optional).
	m := strings.TrimPrefix(step.Method, "/")
	svcPart, methodName, ok := strings.Cut(m, "/")
	if !ok {
		return nil, nil, fmt.Errorf("grpc: method %q must be 'package.Service/Method'", step.Method)
	}
	// svcPart may include a package prefix — walk services to find the match.
	var svcDesc protoreflect.ServiceDescriptor
	for i := 0; i < file.Services().Len(); i++ {
		s := file.Services().Get(i)
		if string(s.FullName()) == svcPart {
			svcDesc = s
			break
		}
	}
	if svcDesc == nil {
		return nil, nil, fmt.Errorf("grpc: service %q not found in schema", svcPart)
	}
	md := svcDesc.Methods().ByName(protoreflect.Name(methodName))
	if md == nil {
		return nil, nil, fmt.Errorf("grpc: method %q not found on %s", methodName, svcPart)
	}
	if md.IsStreamingClient() || md.IsStreamingServer() {
		return nil, nil, fmt.Errorf("grpc: streaming RPC %s/%s not supported yet — unary only for M6-T2", svcPart, methodName)
	}
	return md.Input(), md.Output(), nil
}

func messageToMap(m *dynamicpb.Message) (map[string]any, error) {
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true, UseEnumNumbers: false}.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

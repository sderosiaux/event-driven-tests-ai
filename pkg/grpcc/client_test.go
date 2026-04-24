package grpcc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/grpcc"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Health-check proto matching google.golang.org/grpc/health/grpc_health_v1.
// We compile this inline in the scenario's GRPCStep so grpcc doesn't need
// compile-time stubs; the real server uses the grpc-go stubs for the same
// descriptor.
const healthProto = `
syntax = "proto3";
package grpc.health.v1;

message HealthCheckRequest {
  string service = 1;
}

message HealthCheckResponse {
  enum ServingStatus {
    UNKNOWN = 0;
    SERVING = 1;
    NOT_SERVING = 2;
    SERVICE_UNKNOWN = 3;
  }
  ServingStatus status = 1;
}

service Health {
  rpc Check(HealthCheckRequest) returns (HealthCheckResponse);
}
`

// startHealthServer binds to localhost:0, registers the standard gRPC health
// service marking "edt" as SERVING, and returns the host:port the client
// should dial.
func startHealthServer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("edt", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(s, hs)

	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

func TestGRPCInvokeHealthCheck(t *testing.T) {
	addr := startHealthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := grpcc.Dial(ctx, &scenario.GRPCConnector{Address: addr})
	require.NoError(t, err)
	defer cli.Close()

	resp, err := cli.Invoke(ctx, &scenario.GRPCStep{
		Proto:   healthProto,
		Method:  "grpc.health.v1.Health/Check",
		Request: `{"service":"edt"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, resp.Code, "OK expected, got %d: %s", resp.Code, resp.Message)
	assert.Equal(t, "SERVING", resp.Body["status"])
}

func TestGRPCInvokeReportsNonOKCodeInResponse(t *testing.T) {
	// Dial to a TCP port that isn't listening — the client returns a status
	// error that grpcc surfaces as Response.Code rather than a Go error.
	cli, err := grpcc.Dial(context.Background(), &scenario.GRPCConnector{Address: "127.0.0.1:1"})
	require.NoError(t, err)
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	resp, err := cli.Invoke(ctx, &scenario.GRPCStep{
		Proto:   healthProto,
		Method:  "grpc.health.v1.Health/Check",
		Request: `{"service":"edt"}`,
	})
	require.NoError(t, err, "connection failures must be reported via Response.Code, not bubbled as a Go error")
	assert.NotEqual(t, 0, resp.Code, "unreachable server must produce a non-OK status")
}

func TestGRPCRejectsStreamingMethods(t *testing.T) {
	addr := startHealthServer(t)
	cli, err := grpcc.Dial(context.Background(), &scenario.GRPCConnector{Address: addr})
	require.NoError(t, err)
	defer cli.Close()

	streamingProto := `syntax = "proto3";
package ex;
message Req {}
message Resp {}
service S { rpc Stream(stream Req) returns (stream Resp); }
`
	_, err = cli.Invoke(context.Background(), &scenario.GRPCStep{
		Proto:   streamingProto,
		Method:  "ex.S/Stream",
		Request: "{}",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "streaming")
}

func TestGRPCRejectsUnknownMethod(t *testing.T) {
	addr := startHealthServer(t)
	cli, err := grpcc.Dial(context.Background(), &scenario.GRPCConnector{Address: addr})
	require.NoError(t, err)
	defer cli.Close()

	_, err = cli.Invoke(context.Background(), &scenario.GRPCStep{
		Proto:   healthProto,
		Method:  "grpc.health.v1.Health/Nope",
		Request: "{}",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGRPCRequiresProtoOrProtoFile(t *testing.T) {
	cli, err := grpcc.Dial(context.Background(), &scenario.GRPCConnector{Address: "127.0.0.1:1"})
	require.NoError(t, err)
	defer cli.Close()

	_, err = cli.Invoke(context.Background(), &scenario.GRPCStep{
		Method:  "pkg.Svc/Method",
		Request: "{}",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "proto")
}

func TestGRPCDialRejectsEmptyAddress(t *testing.T) {
	_, err := grpcc.Dial(context.Background(), &scenario.GRPCConnector{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address")
}

// Sanity: the Port adapter closes underlying conns.
func TestGRPCPortReusesAndClosesConn(t *testing.T) {
	addr := startHealthServer(t)
	port := grpcc.NewPort()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Two sequential invocations reuse the same connection.
	for i := 0; i < 2; i++ {
		resp, err := port.Invoke(ctx, &scenario.GRPCConnector{Address: addr}, &scenario.GRPCStep{
			Proto:   healthProto,
			Method:  "grpc.health.v1.Health/Check",
			Request: `{"service":"edt"}`,
		})
		require.NoError(t, err)
		assert.Equal(t, 0, resp.Code)
	}
	port.Close()
	port.Close() // idempotent
}

// Silence unused-import warnings when the insecure import path is pruned.
var _ = insecure.NewCredentials

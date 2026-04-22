package reporter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/report"
	"github.com/event-driven-tests-ai/edt/pkg/reporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushReportPostsToRunsEndpoint(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := reporter.New(srv.URL, "tok-xyz")
	err := c.PushReport(context.Background(), &report.Report{
		Scenario: "demo", RunID: "r-1", Mode: "run",
		StartedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/runs", gotPath)
	assert.Equal(t, "Bearer tok-xyz", gotAuth)
	assert.Equal(t, "application/json", gotCT)
	assert.Contains(t, string(gotBody), `"run_id":"r-1"`)
}

func TestPushReportSurfacesNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()
	c := reporter.New(srv.URL, "")
	err := c.PushReport(context.Background(), &report.Report{Scenario: "x", RunID: "r"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestPushReportNetworkErrorIsExposed(t *testing.T) {
	c := reporter.New("http://127.0.0.1:1", "")
	err := c.PushReport(context.Background(), &report.Report{Scenario: "x", RunID: "r"})
	require.Error(t, err)
	// The CLI is responsible for logging this and NOT changing exit code.
	var ne *NoOp
	_ = errors.As(err, &ne) // type assertion is irrelevant; just ensure error is a real error
}

func TestPushReportEmptyBaseURL(t *testing.T) {
	c := reporter.New("", "")
	err := c.PushReport(context.Background(), &report.Report{})
	require.Error(t, err)
}

// NoOp exists only to anchor the errors.As call above; it is never returned.
type NoOp struct{}

func (NoOp) Error() string { return "noop" }

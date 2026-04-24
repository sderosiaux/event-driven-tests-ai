package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane"
	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeScenario writes a temporary scenario file pointed at the given server URL.
func writeScenario(t *testing.T, baseURL, checksYAML string) string {
	t.Helper()
	body := "apiVersion: edt.io/v1\n" +
		"kind: Scenario\n" +
		"metadata:\n  name: cli-http-test\n" +
		"spec:\n" +
		"  connectors:\n" +
		"    http:\n" +
		"      base_url: " + baseURL + "\n" +
		"  steps:\n" +
		"    - name: ping\n" +
		"      http:\n" +
		"        method: GET\n" +
		"        path: /ok\n" +
		"        expect:\n          status: 200\n" +
		"  checks:\n" + checksYAML
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestEdtRunPassesWhenHTTPOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	path := writeScenario(t, srv.URL, "    - name: trivially_true\n      expr: \"true\"\n      severity: critical\n")

	var stdout, stderr bytes.Buffer
	err := doRun(context.Background(), &stdout, &stderr, &runFlags{
		file:   path,
		format: "json",
	})
	require.NoError(t, err, "stdout=%s stderr=%s", stdout.String(), stderr.String())
	assert.Contains(t, stdout.String(), `"status": "pass"`)
	assert.Contains(t, stdout.String(), `"exit_code": 0`)
}

func TestEdtRunFailsWhenCheckFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	path := writeScenario(t, srv.URL, "    - name: impossible\n      expr: \"false\"\n      severity: critical\n")

	var stdout, stderr bytes.Buffer
	err := doRun(context.Background(), &stdout, &stderr, &runFlags{file: path, format: "json"})
	require.Error(t, err)
	var ex *exitError
	require.True(t, errors.As(err, &ex))
	assert.Equal(t, 1, ex.ExitCode())
	assert.Contains(t, stdout.String(), `"status": "fail"`)
}

func TestEdtRunErrorsOnScenarioFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500) // will make the HTTP expect fail
	}))
	defer srv.Close()

	path := writeScenario(t, srv.URL, "    - name: noop\n      expr: \"true\"\n      severity: info\n")

	var stdout, stderr bytes.Buffer
	err := doRun(context.Background(), &stdout, &stderr, &runFlags{file: path, format: "console"})
	require.Error(t, err, "stdout=%s stderr=%s", stdout.String(), stderr.String())
	var ex *exitError
	require.True(t, errors.As(err, &ex))
	assert.Equal(t, 2, ex.ExitCode())
	assert.Contains(t, stdout.String(), "status:   error")
}

// E2E smoke for M2-T5: CLI run pushes the report to a control-plane HTTP server.
func TestEdtRunPushesReportTo(t *testing.T) {
	httpsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer httpsrv.Close()

	// Launch a real control plane on a random port.
	store := storage.NewMemStore()
	cp := controlplane.NewServerWithStorage(controlplane.Config{}, store)
	cpSrv := httptest.NewServer(cp.Handler())
	defer cpSrv.Close()

	path := writeScenario(t, httpsrv.URL, "    - name: ok\n      expr: \"true\"\n      severity: critical\n")

	var stdout, stderr bytes.Buffer
	err := doRun(context.Background(), &stdout, &stderr, &runFlags{
		file:     path,
		format:   "json",
		reportTo: cpSrv.URL,
	})
	require.NoError(t, err, stdout.String()+stderr.String())

	runs, err := store.ListRuns(context.Background(), "cli-http-test", 10)
	require.NoError(t, err)
	require.Len(t, runs, 1, "control plane should have received the run")
	assert.Equal(t, "pass", runs[0].Status)
}

// Failing push must not change exit code.
func TestEdtRunPushFailureKeepsExitCode(t *testing.T) {
	httpsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer httpsrv.Close()
	path := writeScenario(t, httpsrv.URL, "    - name: ok\n      expr: \"true\"\n      severity: critical\n")
	var stdout, stderr bytes.Buffer
	err := doRun(context.Background(), &stdout, &stderr, &runFlags{
		file: path, format: "json",
		reportTo: "http://127.0.0.1:1", // unreachable
	})
	require.NoError(t, err, "scenario should still pass even when push fails")
	assert.Contains(t, stderr.String(), "warning: push report")
}

func TestEdtRunMissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := doRun(context.Background(), &stdout, &stderr, &runFlags{file: "/nonexistent.yaml"})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "not exist"))
}

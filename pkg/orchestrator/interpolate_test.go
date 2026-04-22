package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpandRunAndPrevious(t *testing.T) {
	c := newInterpCtx("r-123", "2026-04-21T00:00:00Z")
	c.recordPrevious(map[string]any{
		"orderId": "abc",
		"key":     "abc",
		"body":    map[string]any{"status": "ok"},
	})
	cases := map[string]string{
		"plain":                            "plain",
		"/o/${previous.orderId}":           "/o/abc",
		"id=${run.id} order=${previous.orderId}": "id=r-123 order=abc",
		"${previous.body.status}":          "ok",
		"${previous.unknown}":              "${previous.unknown}", // unresolved → literal
		"${weird.no.namespace}":            "${weird.no.namespace}",
	}
	for in, want := range cases {
		assert.Equal(t, want, c.expand(in), "input=%q", in)
	}
}

func TestExpandLeavesNonTemplateAlone(t *testing.T) {
	c := newInterpCtx("r-1", "now")
	for _, s := range []string{"", "no template", "$ alone", "{just braces}"} {
		assert.Equal(t, s, c.expand(s))
	}
}

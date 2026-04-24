package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Codex P1 2026-04-24: os.Expand also recognised $VAR (unbraced), which
// would silently replace $PATH/$HOME inside a CEL literal or payload.
// expandHostEnv now uses a hand-rolled scanner that only touches ${...}
// with ALL_CAPS keys.

func TestExpandHostEnvOnlyTouchesBracedAllCaps(t *testing.T) {
	t.Setenv("FOO", "concrete")

	cases := []struct {
		in   string
		want string
		desc string
	}{
		{"hello ${FOO} world", "hello concrete world", "happy path"},
		{"cel: $FOO unchanged", "cel: $FOO unchanged", "unbraced $VAR must NOT substitute"},
		{"dollars $$ stay", "dollars $$ stay", "literal $$"},
		{"nope ${run.id}", "nope ${run.id}", "scenario interp passes through"},
		{"fn ${uuid()}", "fn ${uuid()}", "helper call passes through"},
		{"mix ${FOO}/${run.id}", "mix concrete/${run.id}", "env expanded, scenario kept"},
		{"unterminated ${FOO", "unterminated ${FOO", "bare '${' at eol"},
		{"starts ${lower}", "starts ${lower}", "lowercase keys not env candidates"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, expandHostEnv(c.in), c.desc)
	}
}

func TestExpandHostEnvPathAndHomeDoNotLeak(t *testing.T) {
	t.Setenv("PATH", "/should/not/appear")
	t.Setenv("HOME", "/neither/should/this")
	src := `match: "path=$PATH home=$HOME"`
	got := expandHostEnv(src)
	assert.Equal(t, src, got, "unbraced env vars must pass through untouched")
}

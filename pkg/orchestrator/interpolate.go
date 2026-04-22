package orchestrator

import (
	"fmt"
	"os"
	"strings"
)

// runVars + stepVars are the contexts available to ${...} interpolation in
// scenario string fields (group, path, payload, header values, etc).
//
// Two namespaces are exposed:
//   - run.id, run.startedAt — scenario-run scoped
//   - previous.<field>      — the last completed step's emitted vars
//     (produced/consumed key, payload fields, http.status, http.body.<k>)
//
// The same vars are also injected into the CEL environment used by match
// rules so authors can write `payload.orderId == previous.orderId`.
type interpCtx struct {
	run      map[string]any
	previous map[string]any
}

func newInterpCtx(runID string, startedAt string) *interpCtx {
	return &interpCtx{
		run:      map[string]any{"id": runID, "startedAt": startedAt},
		previous: map[string]any{},
	}
}

// recordPrevious wipes and repopulates the previous-step namespace.
func (c *interpCtx) recordPrevious(p map[string]any) {
	c.previous = map[string]any{}
	for k, v := range p {
		c.previous[k] = v
	}
}

// expand resolves `${run.X}` and `${previous.X}` references inside s. Unknown
// references stay literal so non-template strings (e.g. JSON body templates)
// pass through untouched.
func (c *interpCtx) expand(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return os.Expand(s, func(key string) string {
		root, rest, ok := strings.Cut(key, ".")
		if !ok {
			return "${" + key + "}"
		}
		var bag map[string]any
		switch root {
		case "run":
			bag = c.run
		case "previous":
			bag = c.previous
		default:
			return "${" + key + "}"
		}
		if v, ok := lookup(bag, rest); ok {
			return fmt.Sprint(v)
		}
		return "${" + key + "}"
	})
}

// lookup walks dotted keys (e.g. "body.status") in a nested map.
func lookup(bag map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = bag
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[p]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

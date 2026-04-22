package orchestrator

import (
	"fmt"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/google/cel-go/cel"
)

// matcher evaluates a list of CEL match rules against a record's payload.
// OR semantics: any rule matching → record matches.
// A nil matcher (no rules) is the caller's signal to fall back to "first record".
type matcher struct {
	programs []cel.Program
}

func compileMatcher(rules []scenario.MatchRule) (*matcher, error) {
	rules = filterNonEmpty(rules)
	if len(rules) == 0 {
		return nil, nil
	}
	env, err := cel.NewEnv(cel.Variable("payload", cel.DynType))
	if err != nil {
		return nil, err
	}
	progs := make([]cel.Program, 0, len(rules))
	for _, r := range rules {
		ast, issues := env.Compile(r.Key)
		if issues != nil && issues.Err() != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Key, issues.Err())
		}
		p, err := env.Program(ast)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", r.Key, err)
		}
		progs = append(progs, p)
	}
	return &matcher{programs: progs}, nil
}

func filterNonEmpty(rs []scenario.MatchRule) []scenario.MatchRule {
	out := rs[:0:0]
	for _, r := range rs {
		if r.Key != "" {
			out = append(out, r)
		}
	}
	return out
}

// matches returns true if any rule evaluates to true on the payload.
func (m *matcher) matches(payload any) (bool, error) {
	for _, p := range m.programs {
		v, _, err := p.Eval(map[string]any{"payload": payload})
		if err != nil {
			return false, err
		}
		b, ok := v.Value().(bool)
		if !ok {
			return false, fmt.Errorf("match rule did not return bool: got %T", v.Value())
		}
		if b {
			return true, nil
		}
	}
	return false, nil
}

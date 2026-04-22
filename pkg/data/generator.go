// Package data implements scenario data generation: deterministic faker-style
// helpers and override resolution for ProduceStep payloads.
//
// The override syntax is intentionally narrow in M1: a value matching the
// pattern `${func(args)}` is dispatched to a registered helper. Future M-passes
// will plumb full CEL expressions into payload values; for now we keep it
// dependency-light and predictable.
package data

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Generator builds a single payload value (typically a map[string]any).
type Generator interface {
	Generate() (map[string]any, error)
}

// Helper is a function exposed to the override DSL: `${name(args...)}`.
// Args arrive as raw strings (already trimmed); the helper does its own parse.
type Helper func(args []string, rng *mrand.Rand) (any, error)

// Registry exposes available helpers. The default Registry is wired with the
// canonical M1 set: uuid, faker.person.id, faker.number.int, faker.number.float.
type Registry struct {
	mu      sync.RWMutex
	helpers map[string]Helper
}

func NewRegistry() *Registry {
	r := &Registry{helpers: make(map[string]Helper)}
	r.RegisterDefaults()
	return r
}

func (r *Registry) Register(name string, h Helper) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.helpers[name] = h
}

func (r *Registry) lookup(name string) (Helper, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.helpers[name]
	return h, ok
}

func (r *Registry) RegisterDefaults() {
	r.Register("uuid", helperUUID)
	r.Register("faker.person.id", helperPersonID)
	r.Register("faker.number.int", helperNumberInt)
	r.Register("faker.number.float", helperNumberFloat)
	r.Register("faker.string.alphanumeric", helperAlphanumeric)
}

// FakerGenerator is the default Generator. It evaluates each override entry
// independently. Same seed → same outputs.
type FakerGenerator struct {
	registry  *Registry
	seed      int64
	overrides map[string]string
	rng       *mrand.Rand
}

func NewFakerGenerator(reg *Registry, seed int64, overrides map[string]string) *FakerGenerator {
	if reg == nil {
		reg = NewRegistry()
	}
	if seed == 0 {
		seed = 1 // explicit non-zero default for reproducibility
	}
	return &FakerGenerator{
		registry:  reg,
		seed:      seed,
		overrides: overrides,
		rng:       mrand.New(mrand.NewSource(seed)),
	}
}

func (g *FakerGenerator) Generate() (map[string]any, error) {
	out := make(map[string]any, len(g.overrides))
	for k, expr := range g.overrides {
		v, err := g.resolve(expr)
		if err != nil {
			return nil, fmt.Errorf("data: override %q: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

// Reseed forces the internal RNG back to its initial state. Useful for tests
// that need to reproduce a generator's output stream.
func (g *FakerGenerator) Reseed() {
	g.rng = mrand.New(mrand.NewSource(g.seed))
}

// ---- Override resolver -----------------------------------------------------

// overrideRE matches `${name(args)}` with greedy args (commas allowed inside
// quoted strings would require a real parser; M1 stays simple).
var overrideRE = regexp.MustCompile(`^\$\{([a-zA-Z_][a-zA-Z0-9_.]*)\(([^)]*)\)\}$`)

func (g *FakerGenerator) resolve(expr string) (any, error) {
	m := overrideRE.FindStringSubmatch(strings.TrimSpace(expr))
	if m == nil {
		// Not a function call: treat as literal string.
		return expr, nil
	}
	name := m[1]
	rawArgs := m[2]
	helper, ok := g.registry.lookup(name)
	if !ok {
		return nil, fmt.Errorf("unknown helper %q", name)
	}
	args := splitArgs(rawArgs)
	return helper(args, g.rng)
}

func splitArgs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// ---- Helpers ---------------------------------------------------------------

func helperUUID(_ []string, rng *mrand.Rand) (any, error) {
	// 16 random bytes from rng (deterministic), formatted as v4-like UUID.
	var b [16]byte
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	hexStr := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32]), nil
}

func helperPersonID(_ []string, rng *mrand.Rand) (any, error) {
	return fmt.Sprintf("usr_%d", rng.Int63n(1_000_000_000)), nil
}

func helperNumberInt(args []string, rng *mrand.Rand) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("faker.number.int(min, max): expected 2 args, got %d", len(args))
	}
	min, err1 := strconv.ParseInt(args[0], 10, 64)
	max, err2 := strconv.ParseInt(args[1], 10, 64)
	if err1 != nil || err2 != nil || max < min {
		return nil, fmt.Errorf("faker.number.int: invalid bounds %v", args)
	}
	return min + rng.Int63n(max-min+1), nil
}

func helperNumberFloat(args []string, rng *mrand.Rand) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("faker.number.float(min, max): expected 2 args, got %d", len(args))
	}
	min, err1 := strconv.ParseFloat(args[0], 64)
	max, err2 := strconv.ParseFloat(args[1], 64)
	if err1 != nil || err2 != nil || max < min {
		return nil, fmt.Errorf("faker.number.float: invalid bounds %v", args)
	}
	return min + rng.Float64()*(max-min), nil
}

func helperAlphanumeric(args []string, rng *mrand.Rand) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("faker.string.alphanumeric(n): expected 1 arg")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 || n > 1024 {
		return nil, fmt.Errorf("faker.string.alphanumeric: invalid length %q", args[0])
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(out), nil
}

// SecureRandomBytes is provided so callers needing non-deterministic randomness
// (cryptographic salts in tests, etc.) don't import crypto/rand themselves.
func SecureRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

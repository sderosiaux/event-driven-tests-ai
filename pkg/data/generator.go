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
	"time"
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
	r.Register("now", helperNow)
	r.Register("faker.person.id", helperPersonID)
	r.Register("faker.number.int", helperNumberInt)
	r.Register("faker.number.float", helperNumberFloat)
	r.Register("faker.string.alphanumeric", helperAlphanumeric)
	r.Register("faker.string.hexadecimal", helperHexadecimal)
	r.Register("faker.string.numeric", helperNumeric)
	r.Register("faker.internet.email", helperEmail)
	r.Register("faker.location.countryCode", helperCountryCode)
	r.Register("faker.lorem.sentence", helperLoremSentence)
	r.Register("faker.helpers.arrayElement", helperArrayElement)
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

// callRE matches the outer `${name(...args)}` shape; the args list itself is
// scanned by a hand-rolled tokenizer so that double-quoted arguments may
// contain commas and parentheses without breaking the parse.
var callRE = regexp.MustCompile(`^\$\{([a-zA-Z_][a-zA-Z0-9_.]*)\((.*)\)\}$`)

func (g *FakerGenerator) resolve(expr string) (any, error) {
	m := callRE.FindStringSubmatch(strings.TrimSpace(expr))
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
	args, err := splitArgs(rawArgs)
	if err != nil {
		return nil, fmt.Errorf("helper %q: %w", name, err)
	}
	return helper(args, g.rng)
}

// splitArgs tokenizes a comma-separated argument list. Double-quoted strings
// are returned without their surrounding quotes and may contain commas, parens,
// and escaped quotes (`\"`). Leading `[` and trailing `]` are accepted so
// fakerjs-style array literals (`arrayElement(["a","b"])`) tokenize the same
// as flat arguments (`arrayElement("a","b")`) — otherwise each element would
// pick up a bracket as part of its string.
func splitArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	if raw == "" {
		return nil, nil
	}
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '\\' && inQuote && i+1 < len(raw):
			cur.WriteByte(raw[i+1])
			i++
		case c == '"':
			inQuote = !inQuote
		case c == ',' && !inQuote:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quoted argument in %q", raw)
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out, nil
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

// ---- Extended faker helpers ------------------------------------------------
//
// These are the minimum set needed to express realistic multi-domain
// scenarios (payments, IoT, experimentation, logs). They match the names of
// the fakerjs API so TS-authored scenarios and YAML-authored scenarios
// converge on one vocabulary.

// helperNow returns the current UTC time in RFC-3339. Non-deterministic by
// design — scenarios that want seeded time should mint it from seeded ints.
func helperNow(_ []string, _ *mrand.Rand) (any, error) {
	return time.Now().UTC().Format(time.RFC3339Nano), nil
}

func helperHexadecimal(args []string, rng *mrand.Rand) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("faker.string.hexadecimal(n): expected 1 arg")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 || n > 1024 {
		return nil, fmt.Errorf("faker.string.hexadecimal: invalid length %q", args[0])
	}
	const alphabet = "0123456789abcdef"
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(out), nil
}

func helperNumeric(args []string, rng *mrand.Rand) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("faker.string.numeric(n): expected 1 arg")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 || n > 1024 {
		return nil, fmt.Errorf("faker.string.numeric: invalid length %q", args[0])
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = byte('0' + rng.Intn(10))
	}
	return string(out), nil
}

// helperEmail mints a plausible email. Deterministic given the seed; no real
// network or dictionary lookup.
func helperEmail(_ []string, rng *mrand.Rand) (any, error) {
	first := commonFirsts[rng.Intn(len(commonFirsts))]
	last := commonLasts[rng.Intn(len(commonLasts))]
	domain := commonDomains[rng.Intn(len(commonDomains))]
	return fmt.Sprintf("%s.%s@%s", first, last, domain), nil
}

// helperCountryCode returns a random ISO-3166 alpha-2 code from a fixed set.
func helperCountryCode(_ []string, rng *mrand.Rand) (any, error) {
	return commonCountries[rng.Intn(len(commonCountries))], nil
}

// helperLoremSentence returns a short random sentence assembled from the
// lorem word list.
func helperLoremSentence(_ []string, rng *mrand.Rand) (any, error) {
	n := 6 + rng.Intn(6) // 6–11 words
	words := make([]string, n)
	for i := range words {
		words[i] = loremWords[rng.Intn(len(loremWords))]
	}
	return strings.ToUpper(words[0][:1]) + words[0][1:] + " " + strings.Join(words[1:], " ") + ".", nil
}

// helperArrayElement picks one of the supplied arguments uniformly. Matches
// fakerjs's helpers.arrayElement([a, b, c]). All args arrive as pre-quoted
// strings from the tokenizer, so we forward the raw value.
func helperArrayElement(args []string, rng *mrand.Rand) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("faker.helpers.arrayElement: needs at least 1 arg")
	}
	return args[rng.Intn(len(args))], nil
}

// Word/name pools. Kept small but plausible; use dedicated fixtures (not
// committed here) if you need cultural diversity or collision resistance.
var (
	commonFirsts    = []string{"alice", "bob", "charlie", "dana", "eve", "frank", "gwen", "hiro", "ingrid", "juan", "kate", "leo", "maya", "nina", "omar", "priya", "quinn", "rhea", "sam", "tara"}
	commonLasts     = []string{"smith", "dupont", "rossi", "nakamura", "alvarez", "tanaka", "silva", "patel", "kim", "ivanov", "okonkwo", "hansen", "mueller", "costa", "romero"}
	commonDomains   = []string{"example.com", "mail.test", "edt.local", "corp.invalid", "demo.invalid"}
	commonCountries = []string{"FR", "US", "DE", "JP", "BR", "IN", "GB", "CA", "AU", "NL", "SE", "PL", "ES", "IT", "MX"}
	loremWords      = []string{"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit", "sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore", "magna", "aliqua", "minim", "veniam", "quis"}
)

package data_test

import (
	mrand "math/rand"
	"strings"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUUIDDeterministic(t *testing.T) {
	a := data.NewFakerGenerator(nil, 42, map[string]string{"id": "${uuid()}"})
	b := data.NewFakerGenerator(nil, 42, map[string]string{"id": "${uuid()}"})

	out1, err := a.Generate()
	require.NoError(t, err)
	out2, err := b.Generate()
	require.NoError(t, err)

	assert.Equal(t, out1["id"], out2["id"], "same seed must produce same UUID")
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, out1["id"])
}

func TestNumberFloatBounds(t *testing.T) {
	g := data.NewFakerGenerator(nil, 7, map[string]string{
		"amount": "${faker.number.float(10, 500)}",
	})
	for i := 0; i < 100; i++ {
		out, err := g.Generate()
		require.NoError(t, err)
		v := out["amount"].(float64)
		assert.GreaterOrEqual(t, v, 10.0)
		assert.LessOrEqual(t, v, 500.0)
	}
}

func TestNumberIntBounds(t *testing.T) {
	g := data.NewFakerGenerator(nil, 1, map[string]string{
		"n": "${faker.number.int(1, 6)}",
	})
	seen := make(map[int64]int)
	for i := 0; i < 200; i++ {
		out, _ := g.Generate()
		seen[out["n"].(int64)]++
	}
	// All faces of the die should show up at least once given N=200.
	for face := int64(1); face <= 6; face++ {
		assert.Greater(t, seen[face], 0, "face %d never seen", face)
	}
}

func TestPersonID(t *testing.T) {
	g := data.NewFakerGenerator(nil, 42, map[string]string{
		"customerId": "${faker.person.id()}",
	})
	out, err := g.Generate()
	require.NoError(t, err)
	assert.Regexp(t, `^usr_\d+$`, out["customerId"])
}

func TestLiteralPassthrough(t *testing.T) {
	g := data.NewFakerGenerator(nil, 1, map[string]string{
		"env": "production",
	})
	out, err := g.Generate()
	require.NoError(t, err)
	assert.Equal(t, "production", out["env"])
}

func TestUnknownHelper(t *testing.T) {
	g := data.NewFakerGenerator(nil, 1, map[string]string{
		"x": "${nope()}",
	})
	_, err := g.Generate()
	require.Error(t, err)
}

func TestInvalidArgs(t *testing.T) {
	g := data.NewFakerGenerator(nil, 1, map[string]string{
		"x": "${faker.number.int(10, 5)}", // max < min
	})
	_, err := g.Generate()
	require.Error(t, err)
}

func TestReseedProducesIdenticalSequence(t *testing.T) {
	g := data.NewFakerGenerator(nil, 100, map[string]string{"id": "${uuid()}"})
	out1, _ := g.Generate()
	g.Reseed()
	out2, _ := g.Generate()
	assert.Equal(t, out1["id"], out2["id"])
}

// Codex finding: quoted strings with commas and parens must round-trip.
func TestQuotedArgsPreserveCommasAndParens(t *testing.T) {
	reg := data.NewRegistry()
	var seen []string
	reg.Register("echo", func(args []string, _ *mrand.Rand) (any, error) {
		seen = args
		return strings.Join(args, "|"), nil
	})
	g := data.NewFakerGenerator(reg, 1, map[string]string{
		"v": `${echo("a,b", "c (nested)", plain, "with \"quote\"")}`,
	})
	_, err := g.Generate()
	require.NoError(t, err)
	require.Equal(t, []string{"a,b", "c (nested)", "plain", `with "quote"`}, seen)
}

func TestUnterminatedQuoteErrors(t *testing.T) {
	g := data.NewFakerGenerator(nil, 1, map[string]string{
		"x": `${faker.number.int("oops, 5)}`,
	})
	_, err := g.Generate()
	require.Error(t, err)
}

func TestCustomHelper(t *testing.T) {
	reg := data.NewRegistry()
	reg.Register("answer", func(_ []string, _ *mrand.Rand) (any, error) { return 42, nil })
	g := data.NewFakerGenerator(reg, 1, map[string]string{"a": "${answer()}"})
	out, err := g.Generate()
	require.NoError(t, err)
	assert.Equal(t, 42, out["a"])
}

// Codex 2026-04-24: new helpers landed for the showcase scenarios.
func TestArrayElementPicksFromEachValue(t *testing.T) {
	reg := data.NewRegistry()
	reg.RegisterDefaults()
	g := data.NewFakerGenerator(reg, 42, map[string]string{
		"v": `${faker.helpers.arrayElement(["A","B","C"])}`,
	})
	counts := map[string]int{}
	for i := 0; i < 120; i++ {
		out, err := g.Generate()
		require.NoError(t, err)
		counts[out["v"].(string)]++
	}
	assert.Greater(t, counts["A"], 0)
	assert.Greater(t, counts["B"], 0)
	assert.Greater(t, counts["C"], 0)
}

// Codex P0 2026-04-24: splitArgs used to strip any outer `[` `]` pair,
// so a single string containing literal brackets lost them. Scope to
// faker-array shape only.
func TestSingleArgWithLiteralBracketsIsPreserved(t *testing.T) {
	seen := ""
	reg := data.NewRegistry()
	reg.Register("capture", func(args []string, _ *mrand.Rand) (any, error) {
		if len(args) > 0 {
			seen = args[0]
		}
		return "ok", nil
	})
	g := data.NewFakerGenerator(reg, 1, map[string]string{
		"v": `${capture("[literal-brackets]")}`,
	})
	_, err := g.Generate()
	require.NoError(t, err)
	assert.Equal(t, "[literal-brackets]", seen)
}

func TestHexadecimalAndNumericHelpers(t *testing.T) {
	reg := data.NewRegistry()
	reg.RegisterDefaults()
	g := data.NewFakerGenerator(reg, 3, map[string]string{
		"hex": `${faker.string.hexadecimal("8")}`,
		"num": `${faker.string.numeric("5")}`,
	})
	out, err := g.Generate()
	require.NoError(t, err)
	hex := out["hex"].(string)
	num := out["num"].(string)
	assert.Len(t, hex, 8)
	assert.Len(t, num, 5)
	for _, r := range hex {
		assert.True(t, (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'))
	}
	for _, r := range num {
		assert.True(t, r >= '0' && r <= '9')
	}
}

func TestEmailAndCountryCodeHelpers(t *testing.T) {
	reg := data.NewRegistry()
	reg.RegisterDefaults()
	g := data.NewFakerGenerator(reg, 1, map[string]string{
		"email":       `${faker.internet.email()}`,
		"countryCode": `${faker.location.countryCode()}`,
	})
	out, err := g.Generate()
	require.NoError(t, err)
	assert.Contains(t, out["email"], "@")
	assert.Len(t, out["countryCode"].(string), 2)
}

func TestNowReturnsRFC3339(t *testing.T) {
	reg := data.NewRegistry()
	reg.RegisterDefaults()
	g := data.NewFakerGenerator(reg, 1, map[string]string{"ts": `${now()}`})
	out, err := g.Generate()
	require.NoError(t, err)
	_, err = time.Parse(time.RFC3339Nano, out["ts"].(string))
	require.NoError(t, err)
}

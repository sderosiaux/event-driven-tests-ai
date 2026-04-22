package data_test

import (
	mrand "math/rand"
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/data"
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

func TestCustomHelper(t *testing.T) {
	reg := data.NewRegistry()
	reg.Register("answer", func(_ []string, _ *mrand.Rand) (any, error) { return 42, nil })
	g := data.NewFakerGenerator(reg, 1, map[string]string{"a": "${answer()}"})
	out, err := g.Generate()
	require.NoError(t, err)
	assert.Equal(t, 42, out["a"])
}

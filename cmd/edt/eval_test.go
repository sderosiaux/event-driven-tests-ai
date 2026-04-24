package main

import (
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProduceOnlyScenarioKeepsOnlyProduceSteps(t *testing.T) {
	in := &scenario.Scenario{
		Spec: scenario.Spec{
			Steps: []scenario.Step{
				{Name: "produce-1", Produce: &scenario.ProduceStep{Topic: "in", Payload: `{"id":"1"}`}},
				{Name: "consume", Consume: &scenario.ConsumeStep{Topic: "out"}},
				{Name: "http", HTTP: &scenario.HTTPStep{Method: "GET", Path: "/healthz"}},
				{Name: "produce-2", Produce: &scenario.ProduceStep{Topic: "in", Payload: `{"id":"2"}`}},
			},
		},
	}

	out := produceOnlyScenario(in)
	require.NotNil(t, out)
	require.Len(t, out.Spec.Steps, 2)
	assert.Equal(t, "produce-1", out.Spec.Steps[0].Name)
	assert.Equal(t, "produce-2", out.Spec.Steps[1].Name)
	assert.Nil(t, out.Spec.Steps[0].Consume)
	assert.Nil(t, out.Spec.Steps[1].HTTP)
}

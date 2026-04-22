package kafka

import (
	"testing"

	"github.com/event-driven-tests-ai/edt/pkg/scenario"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientRequiresBootstrap(t *testing.T) {
	_, err := NewClient(&scenario.KafkaConnector{BootstrapServers: ""}, "", nil)
	require.ErrorContains(t, err, "bootstrap_servers is required")

	_, err = NewClient(nil, "", nil)
	require.ErrorContains(t, err, "nil connector")
}

func TestNewClientBuildsWithoutAuth(t *testing.T) {
	// The client does NOT connect until Ping/Produce — safe to build without a broker.
	c, err := NewClient(&scenario.KafkaConnector{BootstrapServers: "localhost:9092"}, "", nil)
	require.NoError(t, err)
	require.NotNil(t, c)
	c.Close()
}

func TestNewClientWithConsumerGroup(t *testing.T) {
	c, err := NewClient(
		&scenario.KafkaConnector{BootstrapServers: "localhost:9092,broker2:9092"},
		"grp", []string{"t1", "t2"})
	require.NoError(t, err)
	c.Close()
}

func TestSplitServers(t *testing.T) {
	assert.Equal(t, []string{"a:9092"}, splitServers("a:9092"))
	assert.Equal(t, []string{"a:9092", "b:9092"}, splitServers("a:9092,b:9092"))
	assert.Equal(t, []string{"a:9092", "b:9092"}, splitServers("  a:9092 ,  b:9092  "))
	assert.Empty(t, splitServers(""))
}

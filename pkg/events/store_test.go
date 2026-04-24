package events_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendAndQuery(t *testing.T) {
	s := events.NewMemStore(100)
	now := time.Now()
	for i := 0; i < 5; i++ {
		s.Append(events.Event{Stream: "orders", Key: strconv.Itoa(i), Ts: now.Add(time.Duration(i) * time.Millisecond), Direction: events.Produced})
	}
	got := s.Query("orders")
	require.Len(t, got, 5)
	assert.Equal(t, "0", got[0].Key)
	assert.Equal(t, "4", got[4].Key)
	assert.Equal(t, 5, s.Len())
	assert.Equal(t, []string{"orders"}, s.Streams())
	assert.Empty(t, s.Query("unknown"))
}

func TestEvictionFIFO(t *testing.T) {
	s := events.NewMemStore(3)
	for i := 0; i < 5; i++ {
		s.Append(events.Event{Stream: "x", Key: strconv.Itoa(i)})
	}
	got := s.Query("x")
	require.Len(t, got, 3, "cap must be enforced")
	assert.Equal(t, "2", got[0].Key, "oldest should be evicted first")
	assert.Equal(t, "4", got[2].Key)
	assert.Equal(t, 3, s.Len())
}

func TestConcurrentAppend(t *testing.T) {
	s := events.NewMemStore(10_000)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				s.Append(events.Event{Stream: "t", Key: strconv.Itoa(w*100 + i)})
			}
		}(w)
	}
	wg.Wait()
	assert.Equal(t, 800, s.Len())
	assert.Len(t, s.Query("t"), 800)
}

func TestQueryRangeAndSince(t *testing.T) {
	s := events.NewMemStore(0)
	t0 := time.Unix(0, 0)
	for i := 0; i < 5; i++ {
		s.Append(events.Event{Stream: "x", Ts: t0.Add(time.Duration(i) * time.Second)})
	}
	got := s.QueryRange("x", t0.Add(time.Second), t0.Add(3*time.Second))
	require.Len(t, got, 3)

	got = s.Since("x", t0.Add(2*time.Second))
	require.Len(t, got, 3)

	got = s.QueryRange("x", time.Time{}, t0.Add(time.Second))
	require.Len(t, got, 2)
}

func TestAllSortedOrdersByTs(t *testing.T) {
	s := events.NewMemStore(0)
	t0 := time.Unix(0, 0)
	s.Append(events.Event{Stream: "b", Ts: t0.Add(2 * time.Second), Key: "B"})
	s.Append(events.Event{Stream: "a", Ts: t0.Add(time.Second), Key: "A"})
	s.Append(events.Event{Stream: "b", Ts: t0.Add(3 * time.Second), Key: "C"})
	out := s.AllSorted()
	require.Len(t, out, 3)
	assert.Equal(t, "A", out[0].Key)
	assert.Equal(t, "B", out[1].Key)
	assert.Equal(t, "C", out[2].Key)
}

func TestAllAcrossStreams(t *testing.T) {
	s := events.NewMemStore(100)
	s.Append(events.Event{Stream: "a"})
	s.Append(events.Event{Stream: "b"})
	s.Append(events.Event{Stream: "a"})
	assert.Equal(t, 3, s.Len())
	assert.Len(t, s.All(), 3)
	assert.ElementsMatch(t, []string{"a", "b"}, s.Streams())
}

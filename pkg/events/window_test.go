package events_test

import (
	"testing"
	"time"

	"github.com/event-driven-tests-ai/edt/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWindowedViewFiltersByTimestamp(t *testing.T) {
	s := events.NewMemStore(0)
	t0 := time.Unix(0, 0)
	for i := 0; i < 5; i++ {
		s.Append(events.Event{Stream: "x", Ts: t0.Add(time.Duration(i) * time.Second), Key: string(rune('a' + i))})
	}
	w := events.WindowedView(s, t0.Add(2*time.Second))

	got := w.Query("x")
	require.Len(t, got, 3, "events at t=2,3,4 must remain visible")
	assert.Equal(t, "c", got[0].Key)

	assert.Equal(t, 3, w.Len())
	assert.Len(t, w.All(), 3)
	assert.Len(t, w.AllSorted(), 3)
}

func TestWindowedViewWritesPassThrough(t *testing.T) {
	base := events.NewMemStore(0)
	w := events.WindowedView(base, time.Unix(10, 0))
	w.Append(events.Event{Stream: "x", Ts: time.Unix(11, 0), Key: "live"})
	assert.Len(t, base.Query("x"), 1)
	assert.Len(t, w.Query("x"), 1)
}

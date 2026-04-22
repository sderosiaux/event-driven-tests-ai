package events

import "time"

// WindowedView returns a read-only Store that projects another Store through a
// time window: all reads are scoped to events with Ts >= since (since.IsZero
// disables the lower bound). Writes are forwarded to the underlying Store.
//
// This is the canonical way to evaluate watch-mode checks against a sliding
// window without having to change the CEL operators that call Query().
func WindowedView(s Store, since time.Time) Store {
	return &windowed{base: s, since: since}
}

type windowed struct {
	base  Store
	since time.Time
}

func (w *windowed) Append(e Event) { w.base.Append(e) }

func (w *windowed) Query(stream string) []Event {
	return w.base.Since(stream, w.since)
}

func (w *windowed) QueryRange(stream string, from, to time.Time) []Event {
	if !w.since.IsZero() && (from.IsZero() || from.Before(w.since)) {
		from = w.since
	}
	return w.base.QueryRange(stream, from, to)
}

func (w *windowed) Since(stream string, from time.Time) []Event {
	if from.Before(w.since) {
		from = w.since
	}
	return w.base.Since(stream, from)
}

func (w *windowed) Streams() []string { return w.base.Streams() }

func (w *windowed) All() []Event {
	if w.since.IsZero() {
		return w.base.All()
	}
	out := make([]Event, 0)
	for _, name := range w.base.Streams() {
		out = append(out, w.base.Since(name, w.since)...)
	}
	return out
}

func (w *windowed) AllSorted() []Event {
	all := w.All()
	if len(all) <= 1 {
		return all
	}
	// Reuse MemStore's sort by re-using the unsorted slice + a small sort.
	// time.Time.Compare exists since Go 1.20.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].Ts.Compare(all[j-1].Ts) < 0; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return all
}

func (w *windowed) Len() int { return len(w.All()) }

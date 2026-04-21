package events

import "sync"

// DefaultCapPerStream caps how many events are retained per stream
// to bound memory in long-running scenarios.
const DefaultCapPerStream = 100_000

// Store is the interface the orchestrator writes to and the check evaluator reads from.
type Store interface {
	Append(e Event)
	Query(stream string) []Event
	Streams() []string
	All() []Event
	Len() int
}

// MemStore is a thread-safe, capped in-memory Store.
type MemStore struct {
	mu            sync.RWMutex
	capPerStream  int
	byStream      map[string][]Event
	total         int
}

// NewMemStore returns a MemStore with the given per-stream cap.
// A zero or negative cap falls back to DefaultCapPerStream.
func NewMemStore(capPerStream int) *MemStore {
	if capPerStream <= 0 {
		capPerStream = DefaultCapPerStream
	}
	return &MemStore{
		capPerStream: capPerStream,
		byStream:     make(map[string][]Event),
	}
}

func (s *MemStore) Append(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := s.byStream[e.Stream]
	if len(buf) >= s.capPerStream {
		// FIFO eviction: drop oldest.
		copy(buf, buf[1:])
		buf = buf[:len(buf)-1]
		s.total--
	}
	buf = append(buf, e)
	s.byStream[e.Stream] = buf
	s.total++
}

func (s *MemStore) Query(stream string) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.byStream[stream]
	if len(src) == 0 {
		return nil
	}
	out := make([]Event, len(src))
	copy(out, src)
	return out
}

func (s *MemStore) Streams() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.byStream))
	for k := range s.byStream {
		out = append(out, k)
	}
	return out
}

func (s *MemStore) All() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, 0, s.total)
	for _, buf := range s.byStream {
		out = append(out, buf...)
	}
	return out
}

func (s *MemStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total
}

package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// MemStore is an in-memory Storage. It is the default for `edt serve` when
// --db-url is unset and the canonical fixture for unit tests.
type MemStore struct {
	mu          sync.RWMutex
	scenarios   map[string]Scenario          // name → latest version
	runs        map[string]Run               // id → run
	checksByRun map[string][]CheckResult     // run_id → checks
	workers     map[string]Worker            // id → worker
	assignments map[string][]Assignment      // worker_id → assignments
	now         func() time.Time             // overridable for tests
}

func NewMemStore() *MemStore {
	return &MemStore{
		scenarios:   make(map[string]Scenario),
		runs:        make(map[string]Run),
		checksByRun: make(map[string][]CheckResult),
		workers:     make(map[string]Worker),
		assignments: make(map[string][]Assignment),
		now:         func() time.Time { return time.Now().UTC() },
	}
}

func (s *MemStore) Close() error { return nil }

// ---- scenarios -------------------------------------------------------------

func (s *MemStore) UpsertScenario(_ context.Context, name string, yaml []byte, labels map[string]string) (Scenario, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	prev, exists := s.scenarios[name]
	version := 1
	createdAt := now
	if exists {
		version = prev.Version + 1
		createdAt = prev.CreatedAt
	}
	sc := Scenario{
		Name:      name,
		Version:   version,
		YAML:      append([]byte(nil), yaml...),
		Labels:    cloneStrMap(labels),
		CreatedAt: createdAt,
		UpdatedAt: now,
	}
	s.scenarios[name] = sc
	return sc, nil
}

func (s *MemStore) GetScenario(_ context.Context, name string) (Scenario, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.scenarios[name]
	if !ok {
		return Scenario{}, ErrNotFound
	}
	return cloneScenario(sc), nil
}

func (s *MemStore) ListScenarios(_ context.Context) ([]Scenario, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Scenario, 0, len(s.scenarios))
	for _, sc := range s.scenarios {
		out = append(out, cloneScenario(sc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ---- runs ------------------------------------------------------------------

func (s *MemStore) RecordRun(_ context.Context, r Run, checks []CheckResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		r.ID = randID()
	}
	rcopy := r
	rcopy.Report = append([]byte(nil), r.Report...)
	s.runs[r.ID] = rcopy
	cs := make([]CheckResult, len(checks))
	copy(cs, checks)
	for i := range cs {
		cs[i].RunID = r.ID
	}
	s.checksByRun[r.ID] = cs
	return nil
}

func (s *MemStore) GetRun(_ context.Context, id string) (Run, []CheckResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, nil, ErrNotFound
	}
	cs := append([]CheckResult(nil), s.checksByRun[id]...)
	return r, cs, nil
}

func (s *MemStore) ListRuns(_ context.Context, scenario string, limit int) ([]Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Run, 0, len(s.runs))
	for _, r := range s.runs {
		if scenario != "" && r.Scenario != scenario {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemStore) SLOPassRate(_ context.Context, scenario string, window time.Duration) (map[string]float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := s.now().Add(-window)
	type acc struct{ pass, total int }
	bucket := map[string]*acc{}
	for _, r := range s.runs {
		if r.Scenario != scenario || r.StartedAt.Before(cutoff) {
			continue
		}
		for _, c := range s.checksByRun[r.ID] {
			a, ok := bucket[c.Name]
			if !ok {
				a = &acc{}
				bucket[c.Name] = a
			}
			a.total++
			if c.Passed {
				a.pass++
			}
		}
	}
	out := make(map[string]float64, len(bucket))
	for k, a := range bucket {
		if a.total == 0 {
			out[k] = 0
			continue
		}
		out[k] = float64(a.pass) / float64(a.total)
	}
	return out, nil
}

// ---- workers ---------------------------------------------------------------

func (s *MemStore) RegisterWorker(_ context.Context, labels map[string]string, version string) (Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	w := Worker{
		ID:            "w-" + randID(),
		Labels:        cloneStrMap(labels),
		Version:       version,
		RegisteredAt:  now,
		LastHeartbeat: now,
	}
	s.workers[w.ID] = w
	return w, nil
}

func (s *MemStore) Heartbeat(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[id]
	if !ok {
		return ErrNotFound
	}
	w.LastHeartbeat = s.now()
	s.workers[id] = w
	return nil
}

func (s *MemStore) ListWorkers(_ context.Context) ([]Worker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemStore) AssignScenario(_ context.Context, workerID, scenarioName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workers[workerID]; !ok {
		return ErrNotFound
	}
	if _, ok := s.scenarios[scenarioName]; !ok {
		return ErrNotFound
	}
	s.assignments[workerID] = append(s.assignments[workerID], Assignment{
		WorkerID:     workerID,
		ScenarioName: scenarioName,
		AssignedAt:   s.now(),
	})
	return nil
}

func (s *MemStore) ListAssignments(_ context.Context, workerID string) ([]Assignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.assignments[workerID]
	out := make([]Assignment, len(src))
	copy(out, src)
	return out, nil
}

// ---- helpers ---------------------------------------------------------------

func cloneStrMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneScenario(sc Scenario) Scenario {
	out := sc
	out.YAML = append([]byte(nil), sc.YAML...)
	out.Labels = cloneStrMap(sc.Labels)
	return out
}

func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

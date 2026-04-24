package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	tokens      map[string]Token             // sha256(plaintext) → token
	tokensByID  map[string]string            // id → sha256(plaintext) (for revoke)
	samples     []CheckSample                // append-only; scanned linearly by scenario+since
	evalRuns    map[string]EvalRun           // id → run
	evalResults map[string][]EvalResult      // run_id → results
	now         func() time.Time             // overridable for tests
}

func NewMemStore() *MemStore {
	return &MemStore{
		scenarios:   make(map[string]Scenario),
		runs:        make(map[string]Run),
		checksByRun: make(map[string][]CheckResult),
		workers:     make(map[string]Worker),
		assignments: make(map[string][]Assignment),
		tokens:      make(map[string]Token),
		tokensByID:  make(map[string]string),
		evalRuns:    make(map[string]EvalRun),
		evalResults: make(map[string][]EvalResult),
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

// ---- eval runs -------------------------------------------------------------

func (s *MemStore) RecordEvalRun(_ context.Context, r EvalRun, results []EvalResult) error {
	if r.ID == "" {
		return fmt.Errorf("storage: eval run ID is required")
	}
	if r.Scenario == "" {
		return fmt.Errorf("storage: eval run scenario is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evalRuns[r.ID] = r
	rows := make([]EvalResult, len(results))
	copy(rows, results)
	for i := range rows {
		rows[i].RunID = r.ID
	}
	s.evalResults[r.ID] = rows
	return nil
}

func (s *MemStore) GetEvalRun(_ context.Context, id string) (EvalRun, []EvalResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.evalRuns[id]
	if !ok {
		return EvalRun{}, nil, ErrNotFound
	}
	rows := append([]EvalResult(nil), s.evalResults[id]...)
	return r, rows, nil
}

func (s *MemStore) ListEvalRuns(_ context.Context, scenario string, limit int) ([]EvalRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EvalRun, 0, len(s.evalRuns))
	for _, r := range s.evalRuns {
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

// ---- tokens ----------------------------------------------------------------
//
// Tokens are indexed by the sha256 hex digest of their plaintext. The
// in-memory MemStore keeps the plaintext alongside each entry only so
// IssueTokenWithPlaintext can stay idempotent for bootstrap flows; that
// field is never returned by LookupToken/ListTokens.

func (s *MemStore) IssueToken(_ context.Context, role Role, note string) (Token, error) {
	pt := "edt_" + randID() + randID()
	return s.issueLocked(pt, role, note)
}

func (s *MemStore) IssueTokenWithPlaintext(_ context.Context, plaintext string, role Role, note string) (Token, error) {
	if plaintext == "" {
		return Token{}, fmt.Errorf("storage: empty token plaintext")
	}
	return s.issueLocked(plaintext, role, note)
}

func (s *MemStore) issueLocked(plaintext string, role Role, note string) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	digest := hashToken(plaintext)
	if existing, exists := s.tokens[digest]; exists {
		existing.Plaintext = plaintext // echoed once, only to the caller
		return existing, nil
	}
	tok := Token{
		ID:        "tok_" + randID(),
		Plaintext: plaintext,
		Role:      role,
		Note:      note,
		CreatedAt: s.now(),
	}
	s.tokens[digest] = tok
	s.tokensByID[tok.ID] = digest
	return tok, nil
}

func (s *MemStore) LookupToken(_ context.Context, plaintext string) (Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokens[hashToken(plaintext)]
	if !ok {
		return Token{}, ErrNotFound
	}
	out := t
	out.Plaintext = "" // never re-emit through Lookup
	return out, nil
}

func (s *MemStore) ListTokens(_ context.Context) ([]Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Token, 0, len(s.tokens))
	for _, t := range s.tokens {
		t.Plaintext = ""
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemStore) RevokeToken(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	digest, ok := s.tokensByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.tokens, digest)
	delete(s.tokensByID, id)
	return nil
}

// ---- check samples (watch-mode resume) ------------------------------------

func (s *MemStore) AppendCheckSamples(_ context.Context, samples []CheckSample) error {
	if len(samples) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, samples...)
	return nil
}

func (s *MemStore) LoadCheckSamplesSince(_ context.Context, scenario string, since time.Time) ([]CheckSample, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CheckSample, 0)
	for _, sample := range s.samples {
		if sample.Scenario != scenario {
			continue
		}
		if !since.IsZero() && sample.Ts.Before(since) {
			continue
		}
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ts.Before(out[j].Ts) })
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

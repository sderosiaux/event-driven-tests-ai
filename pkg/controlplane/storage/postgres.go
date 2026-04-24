package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the production Storage backed by Postgres.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore connects, runs migrations, and returns a ready PGStore.
func NewPGStore(ctx context.Context, dsn string) (*PGStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: ping: %w", err)
	}
	if err := applyMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &PGStore{pool: pool}, nil
}

func (s *PGStore) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// ---- scenarios -------------------------------------------------------------

func (s *PGStore) UpsertScenario(ctx context.Context, name string, yaml []byte, labels map[string]string) (Scenario, error) {
	labelsJSON, _ := json.Marshal(labels)
	now := time.Now().UTC()
	var sc Scenario
	err := s.pool.QueryRow(ctx, `
        INSERT INTO scenarios (name, version, yaml, labels, created_at, updated_at)
        VALUES ($1, 1, $2, $3, $4, $4)
        ON CONFLICT (name) DO UPDATE
            SET version    = scenarios.version + 1,
                yaml       = EXCLUDED.yaml,
                labels     = EXCLUDED.labels,
                updated_at = EXCLUDED.updated_at
        RETURNING name, version, yaml, labels, created_at, updated_at
    `, name, yaml, labelsJSON, now).Scan(&sc.Name, &sc.Version, &sc.YAML, &labelsJSON, &sc.CreatedAt, &sc.UpdatedAt)
	if err != nil {
		return Scenario{}, err
	}
	if err := json.Unmarshal(labelsJSON, &sc.Labels); err != nil {
		sc.Labels = map[string]string{}
	}
	return sc, nil
}

func (s *PGStore) GetScenario(ctx context.Context, name string) (Scenario, error) {
	var sc Scenario
	var labelsJSON []byte
	err := s.pool.QueryRow(ctx, `
        SELECT name, version, yaml, labels, created_at, updated_at
        FROM scenarios WHERE name = $1
    `, name).Scan(&sc.Name, &sc.Version, &sc.YAML, &labelsJSON, &sc.CreatedAt, &sc.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Scenario{}, ErrNotFound
	}
	if err != nil {
		return Scenario{}, err
	}
	_ = json.Unmarshal(labelsJSON, &sc.Labels)
	return sc, nil
}

func (s *PGStore) ListScenarios(ctx context.Context) ([]Scenario, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT name, version, yaml, labels, created_at, updated_at
        FROM scenarios ORDER BY name
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Scenario
	for rows.Next() {
		var sc Scenario
		var labelsJSON []byte
		if err := rows.Scan(&sc.Name, &sc.Version, &sc.YAML, &labelsJSON, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(labelsJSON, &sc.Labels)
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ---- runs ------------------------------------------------------------------

func (s *PGStore) RecordRun(ctx context.Context, r Run, checks []CheckResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if r.ID == "" {
		r.ID = randID()
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO runs (id, scenario, mode, status, exit_code, started_at, finished_at, duration_ns, report)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (id) DO UPDATE
            SET scenario    = EXCLUDED.scenario,
                mode        = EXCLUDED.mode,
                status      = EXCLUDED.status,
                exit_code   = EXCLUDED.exit_code,
                finished_at = EXCLUDED.finished_at,
                duration_ns = EXCLUDED.duration_ns,
                report      = EXCLUDED.report
    `, r.ID, r.Scenario, r.Mode, r.Status, r.ExitCode, r.StartedAt, r.FinishedAt, int64(r.Duration), r.Report); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM check_results WHERE run_id = $1`, r.ID); err != nil {
		return err
	}
	for _, c := range checks {
		if _, err := tx.Exec(ctx, `
            INSERT INTO check_results (run_id, name, severity, "window", passed, value, "error", at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        `, r.ID, c.Name, c.Severity, c.Window, c.Passed, c.Value, c.Err, c.At); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PGStore) GetRun(ctx context.Context, id string) (Run, []CheckResult, error) {
	var r Run
	var dur int64
	err := s.pool.QueryRow(ctx, `
        SELECT id, scenario, mode, status, exit_code, started_at, finished_at, duration_ns, report
        FROM runs WHERE id = $1
    `, id).Scan(&r.ID, &r.Scenario, &r.Mode, &r.Status, &r.ExitCode, &r.StartedAt, &r.FinishedAt, &dur, &r.Report)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, nil, ErrNotFound
	}
	if err != nil {
		return Run{}, nil, err
	}
	r.Duration = time.Duration(dur)

	rows, err := s.pool.Query(ctx, `
        SELECT name, severity, "window", passed, value, "error", at
        FROM check_results WHERE run_id = $1 ORDER BY at
    `, id)
	if err != nil {
		return r, nil, err
	}
	defer rows.Close()
	var checks []CheckResult
	for rows.Next() {
		var c CheckResult
		c.RunID = id
		var window, value, errStr *string
		if err := rows.Scan(&c.Name, &c.Severity, &window, &c.Passed, &value, &errStr, &c.At); err != nil {
			return r, nil, err
		}
		c.Window = derefStr(window)
		c.Value = derefStr(value)
		c.Err = derefStr(errStr)
		checks = append(checks, c)
	}
	return r, checks, rows.Err()
}

func (s *PGStore) ListRuns(ctx context.Context, scenario string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows pgx.Rows
		err  error
	)
	if scenario == "" {
		rows, err = s.pool.Query(ctx, `
            SELECT id, scenario, mode, status, exit_code, started_at, finished_at, duration_ns
            FROM runs ORDER BY started_at DESC LIMIT $1
        `, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
            SELECT id, scenario, mode, status, exit_code, started_at, finished_at, duration_ns
            FROM runs WHERE scenario = $1 ORDER BY started_at DESC LIMIT $2
        `, scenario, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var dur int64
		if err := rows.Scan(&r.ID, &r.Scenario, &r.Mode, &r.Status, &r.ExitCode, &r.StartedAt, &r.FinishedAt, &dur); err != nil {
			return nil, err
		}
		r.Duration = time.Duration(dur)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) AppendCheckSamples(ctx context.Context, samples []CheckSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, sample := range samples {
		if _, err := tx.Exec(ctx, `
            INSERT INTO check_samples (scenario_name, check_name, ts, passed, value, severity, "window")
            VALUES ($1, $2, $3, $4, $5, $6, $7)
        `, sample.Scenario, sample.Check, sample.Ts, sample.Passed, sample.Value, sample.Severity, sample.Window); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PGStore) LoadCheckSamplesSince(ctx context.Context, scenario string, since time.Time) ([]CheckSample, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT scenario_name, check_name, ts, passed, value, severity, "window"
        FROM check_samples
        WHERE scenario_name = $1 AND ts >= $2
        ORDER BY ts
    `, scenario, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckSample
	for rows.Next() {
		var sample CheckSample
		var value, window *string
		if err := rows.Scan(&sample.Scenario, &sample.Check, &sample.Ts, &sample.Passed, &value, &sample.Severity, &window); err != nil {
			return nil, err
		}
		sample.Value = derefStr(value)
		sample.Window = derefStr(window)
		out = append(out, sample)
	}
	return out, rows.Err()
}

func (s *PGStore) SLOPassRate(ctx context.Context, scenario string, window time.Duration) (map[string]float64, error) {
	cutoff := time.Now().Add(-window)
	rows, err := s.pool.Query(ctx, `
        SELECT cr.name,
               AVG(CASE WHEN cr.passed THEN 1.0 ELSE 0.0 END)::float8
        FROM check_results cr
        JOIN runs r ON r.id = cr.run_id
        WHERE r.scenario = $1 AND r.started_at >= $2
        GROUP BY cr.name
    `, scenario, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var name string
		var rate float64
		if err := rows.Scan(&name, &rate); err != nil {
			return nil, err
		}
		out[name] = rate
	}
	return out, rows.Err()
}

// ---- eval runs -------------------------------------------------------------

func (s *PGStore) RecordEvalRun(ctx context.Context, r EvalRun, results []EvalResult) error {
	if r.ID == "" {
		return fmt.Errorf("storage: eval run ID is required")
	}
	if r.Scenario == "" {
		return fmt.Errorf("storage: eval run scenario is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
        INSERT INTO eval_runs (id, scenario, judge_model, iterations, started_at, finished_at, status)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (id) DO UPDATE
            SET scenario    = EXCLUDED.scenario,
                judge_model = EXCLUDED.judge_model,
                iterations  = EXCLUDED.iterations,
                started_at  = EXCLUDED.started_at,
                finished_at = EXCLUDED.finished_at,
                status      = EXCLUDED.status
    `, r.ID, r.Scenario, r.JudgeModel, r.Iterations, r.StartedAt, r.FinishedAt, r.Status); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM eval_results WHERE run_id = $1`, r.ID); err != nil {
		return err
	}
	for _, res := range results {
		if _, err := tx.Exec(ctx, `
            INSERT INTO eval_results (run_id, name, aggregate, samples, required_samples, value, threshold, passed, status, errors)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
        `, r.ID, res.Name, res.Aggregate, res.Samples, res.RequiredSamples, res.Value, res.Threshold, res.Passed, res.Status, res.Errors); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PGStore) GetEvalRun(ctx context.Context, id string) (EvalRun, []EvalResult, error) {
	var r EvalRun
	err := s.pool.QueryRow(ctx, `
        SELECT id, scenario, judge_model, iterations, started_at, finished_at, status
        FROM eval_runs WHERE id = $1
    `, id).Scan(&r.ID, &r.Scenario, &r.JudgeModel, &r.Iterations, &r.StartedAt, &r.FinishedAt, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return EvalRun{}, nil, ErrNotFound
	}
	if err != nil {
		return EvalRun{}, nil, err
	}
	rows, err := s.pool.Query(ctx, `
        SELECT name, aggregate, samples, required_samples, value, threshold, passed, status, errors
        FROM eval_results WHERE run_id = $1 ORDER BY name
    `, id)
	if err != nil {
		return r, nil, err
	}
	defer rows.Close()
	var out []EvalResult
	for rows.Next() {
		var res EvalResult
		res.RunID = id
		if err := rows.Scan(&res.Name, &res.Aggregate, &res.Samples, &res.RequiredSamples, &res.Value, &res.Threshold, &res.Passed, &res.Status, &res.Errors); err != nil {
			return r, nil, err
		}
		out = append(out, res)
	}
	return r, out, rows.Err()
}

func (s *PGStore) ListEvalRuns(ctx context.Context, scenario string, limit int) ([]EvalRun, error) {
	if limit <= 0 {
		limit = 100
	}
	var (
		rows pgx.Rows
		err  error
	)
	if scenario == "" {
		rows, err = s.pool.Query(ctx, `
            SELECT id, scenario, judge_model, iterations, started_at, finished_at, status
            FROM eval_runs ORDER BY started_at DESC LIMIT $1
        `, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
            SELECT id, scenario, judge_model, iterations, started_at, finished_at, status
            FROM eval_runs WHERE scenario = $1 ORDER BY started_at DESC LIMIT $2
        `, scenario, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvalRun
	for rows.Next() {
		var r EvalRun
		if err := rows.Scan(&r.ID, &r.Scenario, &r.JudgeModel, &r.Iterations, &r.StartedAt, &r.FinishedAt, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- workers ---------------------------------------------------------------

func (s *PGStore) RegisterWorker(ctx context.Context, labels map[string]string, version string) (Worker, error) {
	id := "w-" + randID()
	now := time.Now().UTC()
	labelsJSON, _ := json.Marshal(labels)
	if _, err := s.pool.Exec(ctx, `
        INSERT INTO workers (id, labels, version, registered_at, last_heartbeat)
        VALUES ($1, $2, $3, $4, $4)
    `, id, labelsJSON, version, now); err != nil {
		return Worker{}, err
	}
	return Worker{
		ID: id, Labels: cloneStrMap(labels), Version: version,
		RegisteredAt: now, LastHeartbeat: now,
	}, nil
}

func (s *PGStore) Heartbeat(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE workers SET last_heartbeat = NOW() WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PGStore) ListWorkers(ctx context.Context) ([]Worker, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id, labels, version, registered_at, last_heartbeat
        FROM workers ORDER BY id
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		var w Worker
		var labelsJSON []byte
		if err := rows.Scan(&w.ID, &labelsJSON, &w.Version, &w.RegisteredAt, &w.LastHeartbeat); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(labelsJSON, &w.Labels)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *PGStore) AssignScenario(ctx context.Context, workerID, scenarioName string) error {
	// Pre-check existence to translate FK violations into ErrNotFound the API
	// already maps to 404.
	if _, err := s.GetScenario(ctx, scenarioName); err != nil {
		return err
	}
	var existsW bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workers WHERE id = $1)`, workerID).Scan(&existsW); err != nil {
		return err
	}
	if !existsW {
		return ErrNotFound
	}
	_, err := s.pool.Exec(ctx, `
        INSERT INTO assignments (worker_id, scenario_name, assigned_at)
        VALUES ($1, $2, NOW())
        ON CONFLICT DO NOTHING
    `, workerID, scenarioName)
	return err
}

func (s *PGStore) ListAssignments(ctx context.Context, workerID string) ([]Assignment, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT worker_id, scenario_name, assigned_at FROM assignments
        WHERE worker_id = $1 ORDER BY assigned_at
    `, workerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.WorkerID, &a.ScenarioName, &a.AssignedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- tokens ----------------------------------------------------------------

func (s *PGStore) IssueToken(ctx context.Context, role Role, note string) (Token, error) {
	pt := "edt_" + randID() + randID()
	return s.IssueTokenWithPlaintext(ctx, pt, role, note)
}

func (s *PGStore) IssueTokenWithPlaintext(ctx context.Context, plaintext string, role Role, note string) (Token, error) {
	digest := hashToken(plaintext)
	newID := "tok_" + randID()
	// Atomic upsert-if-absent: the CTE inserts-or-does-nothing then always
	// falls back to selecting the canonical row. Removes the race between the
	// old two-statement INSERT + SELECT.
	row := s.pool.QueryRow(ctx, `
        WITH ins AS (
            INSERT INTO tokens (id, plaintext_sha256, role, note, created_at)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (plaintext_sha256) DO NOTHING
            RETURNING id, role, note, created_at
        )
        SELECT id, role, note, created_at FROM ins
        UNION ALL
        SELECT id, role, note, created_at FROM tokens WHERE plaintext_sha256 = $2
        LIMIT 1
    `, newID, digest, string(role), note, time.Now().UTC())

	var stored Token
	var roleStr string
	if err := row.Scan(&stored.ID, &roleStr, &stored.Note, &stored.CreatedAt); err != nil {
		return Token{}, err
	}
	stored.Role = Role(roleStr)
	stored.Plaintext = plaintext
	return stored, nil
}

func (s *PGStore) LookupToken(ctx context.Context, plaintext string) (Token, error) {
	var t Token
	var roleStr string
	err := s.pool.QueryRow(ctx, `SELECT id, role, note, created_at FROM tokens WHERE plaintext_sha256 = $1`, hashToken(plaintext)).
		Scan(&t.ID, &roleStr, &t.Note, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Token{}, ErrNotFound
	}
	if err != nil {
		return Token{}, err
	}
	t.Role = Role(roleStr)
	return t, nil
}

func (s *PGStore) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, role, note, created_at FROM tokens ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var roleStr string
		if err := rows.Scan(&t.ID, &roleStr, &t.Note, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Role = Role(roleStr)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) RevokeToken(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM tokens WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// randHex returns a hex-encoded random byte string of the given byte length.
// Kept here so postgres.go does not need to import internal helpers from memory.go.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

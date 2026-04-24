-- 0004_eval_runs: first-class ingest for `edt eval` reports.
--
-- Prior code path pushed opaque JSON into runs.report; there was no way to
-- query "which evals are drifting below threshold over the last 24h" without
-- re-parsing the raw blob every time.

CREATE TABLE IF NOT EXISTS eval_runs (
    id           TEXT PRIMARY KEY,
    scenario     TEXT NOT NULL,
    judge_model  TEXT NOT NULL DEFAULT '',
    iterations   INTEGER NOT NULL DEFAULT 0,
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL,
    status       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS eval_runs_scenario_started_at_idx
    ON eval_runs (scenario, started_at DESC);

CREATE TABLE IF NOT EXISTS eval_results (
    run_id           TEXT NOT NULL REFERENCES eval_runs(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    aggregate        TEXT NOT NULL DEFAULT '',
    samples          INTEGER NOT NULL DEFAULT 0,
    required_samples INTEGER NOT NULL DEFAULT 0,
    value            DOUBLE PRECISION NOT NULL DEFAULT 0,
    threshold        TEXT NOT NULL DEFAULT '',
    passed           BOOLEAN NOT NULL,
    status           TEXT NOT NULL,
    errors           INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (run_id, name)
);

CREATE INDEX IF NOT EXISTS eval_results_name_status_idx
    ON eval_results (name, status);

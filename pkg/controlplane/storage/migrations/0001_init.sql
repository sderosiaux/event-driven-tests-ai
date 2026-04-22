-- 0001_init: scenarios, runs, check_results, workers, assignments, tokens.

CREATE TABLE IF NOT EXISTS scenarios (
    name        TEXT PRIMARY KEY,
    version     INTEGER NOT NULL,
    yaml        BYTEA NOT NULL,
    labels      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS runs (
    id           TEXT PRIMARY KEY,
    scenario     TEXT NOT NULL,
    mode         TEXT NOT NULL DEFAULT 'run',
    status       TEXT NOT NULL,
    exit_code    INTEGER NOT NULL DEFAULT 0,
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL,
    duration_ns  BIGINT NOT NULL DEFAULT 0,
    report       BYTEA
);

CREATE INDEX IF NOT EXISTS runs_scenario_started_at_idx
    ON runs (scenario, started_at DESC);

CREATE TABLE IF NOT EXISTS check_results (
    run_id    TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    name      TEXT NOT NULL,
    severity  TEXT NOT NULL DEFAULT 'warning',
    "window"  TEXT,
    passed    BOOLEAN NOT NULL,
    value     TEXT,
    "error"   TEXT,
    at        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (run_id, name)
);

CREATE TABLE IF NOT EXISTS workers (
    id              TEXT PRIMARY KEY,
    labels          JSONB NOT NULL DEFAULT '{}'::jsonb,
    version         TEXT,
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_heartbeat  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS assignments (
    worker_id     TEXT NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    scenario_name TEXT NOT NULL REFERENCES scenarios(name) ON DELETE CASCADE,
    assigned_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (worker_id, scenario_name)
);

CREATE TABLE IF NOT EXISTS tokens (
    id          TEXT PRIMARY KEY,
    plaintext   TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

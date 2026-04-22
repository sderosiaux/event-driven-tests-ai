-- 0003: per-check sliding-window samples used to seed a worker's state after
-- a restart so a freshly-resumed watch-mode scenario does not start with a
-- cold window (see design spec §7.2 and M2 acceptance criterion §5).

CREATE TABLE IF NOT EXISTS check_samples (
    scenario_name TEXT        NOT NULL,
    check_name    TEXT        NOT NULL,
    ts            TIMESTAMPTZ NOT NULL,
    passed        BOOLEAN     NOT NULL,
    value         TEXT,
    severity      TEXT        NOT NULL DEFAULT 'warning',
    "window"      TEXT
);

CREATE INDEX IF NOT EXISTS check_samples_scenario_ts_idx
    ON check_samples (scenario_name, ts DESC);

CREATE INDEX IF NOT EXISTS check_samples_scenario_check_ts_idx
    ON check_samples (scenario_name, check_name, ts DESC);

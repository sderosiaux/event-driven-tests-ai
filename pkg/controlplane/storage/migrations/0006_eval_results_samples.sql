-- 0006_eval_results_samples: per-iteration eval transcripts.
--
-- Stores the (input, agent output, judge score, judge reasoning) tuples
-- behind each eval's aggregate value. Without this column the detail page
-- can only show the summary number, not WHY it landed where it did.
--
-- JSONB so we can index/filter individual transcripts later if needed.
-- NULL/empty = no samples persisted (legacy rows or executors that didn't
-- record them).

ALTER TABLE eval_results
    ADD COLUMN IF NOT EXISTS samples_json JSONB;

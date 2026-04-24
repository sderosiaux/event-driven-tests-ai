-- 0005_eval_results_judge_model: record the judge model per eval row.
--
-- eval_runs.judge_model is the run-level rollup ("mixed" or the single uniform
-- model); eval_results.judge_model is the provenance for that specific eval.
-- Scenarios can declare multiple evals with different models, and collapsing
-- that to one string at the run level hid which eval was produced by which.

ALTER TABLE eval_results
    ADD COLUMN IF NOT EXISTS judge_model TEXT NOT NULL DEFAULT '';

-- 0002: tokens are indexed by sha256(plaintext) instead of the plaintext.
--
-- For a first-cut deployment we simply rename the column; rows that were
-- populated under 0001 carried the plaintext in that column and must be
-- re-issued after this migration (the previous schema was never used in
-- production — this is an OSS pre-release).

ALTER TABLE tokens RENAME COLUMN plaintext TO plaintext_sha256;

-- Existing UNIQUE constraint on the renamed column is preserved by ALTER TABLE
-- RENAME COLUMN (Postgres rewrites constraint references). No extra index.

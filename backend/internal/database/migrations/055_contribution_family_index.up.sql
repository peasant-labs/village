-- Serves the contribution family expansion. The project picker, the paged view
-- and the project snapshot each descend a session family by joining the owner's
-- transcripts whose parent_session_id names a session already reached. Without
-- an index on this pair, that join is planned from table statistics alone.
--
-- A plain CREATE INDEX, not CONCURRENTLY: each migration runs inside one
-- transaction (runMigration in migrate.go), and CONCURRENTLY cannot run inside a
-- transaction block. The build therefore holds a SHARE lock on transcripts for
-- its whole duration: inserts, updates and deletes on transcripts wait for it,
-- while reads do not. On the benchmark host the build took 0.66 to 2.70 s at
-- 1,000,000 rows, and it grows with the table, so measure it on a copy of
-- production data before a large deployment.
CREATE INDEX idx_transcripts_owner_parent_session
    ON transcripts (owner_id, parent_session_id);

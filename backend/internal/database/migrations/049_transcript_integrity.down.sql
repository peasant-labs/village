-- Forward-only in any shared environment: use this only on a development
-- database. It discards every stored result, inherited flag, post-deletion
-- record and check attempt.
DROP TABLE IF EXISTS transcript_integrity_attempts;
DROP TABLE IF EXISTS integrity_flag_tombstones;
DROP TABLE IF EXISTS transcript_integrity_inherited_flags;
DROP TABLE IF EXISTS transcript_integrity_results;

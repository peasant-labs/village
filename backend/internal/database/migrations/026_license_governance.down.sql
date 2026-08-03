-- Reverse 026's FINAL design. Note the accepted asymmetry of a superseding
-- migration: the gen-1 remnants (transcript_governance_events) that 026's up
-- dropped are NOT resurrected here — they belonged to a never-merged branch
-- iteration, had no writer, and are unrecoverable by design.
--
-- Order: triggers first (they reference the functions and tables), then functions,
-- then the audit table, then transcripts.license_id (auto-drops
-- idx_transcripts_license), then the reference tables in FK-dependency order.
DROP TRIGGER IF EXISTS trg_governance_audit_immutable ON transcript_governance_events_audit;
DROP TRIGGER IF EXISTS trg_audit_transcript_retract ON transcripts;
DROP TRIGGER IF EXISTS trg_audit_transcript_governance ON transcripts;
DROP TRIGGER IF EXISTS trg_audit_transcript_publish ON transcripts;
DROP FUNCTION IF EXISTS governance_audit_block_mutation();
DROP FUNCTION IF EXISTS audit_transcript_retract();
DROP FUNCTION IF EXISTS audit_transcript_governance();
DROP TABLE IF EXISTS transcript_governance_events_audit;
ALTER TABLE transcripts DROP COLUMN IF EXISTS license_id;
DROP TABLE IF EXISTS governance_event_types;
DROP TABLE IF EXISTS licenses;

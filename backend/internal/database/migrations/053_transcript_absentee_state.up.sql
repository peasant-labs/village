-- A constant default uses PostgreSQL's fast-default path; the CHECK still
-- validates existing rows while the ALTER TABLE lock is held.
ALTER TABLE transcripts
    ADD COLUMN content_state TEXT NOT NULL DEFAULT 'present',
    ADD CONSTRAINT transcripts_content_state_closed
        CHECK (content_state IN ('present', 'unavailable'));

-- The transcript is a retained value, not a foreign key. Its governance audit
-- event carries the retraction time; no second timestamp or owner is stored.
CREATE TABLE transcript_retraction_audiences (
    transcript_id UUID NOT NULL,
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (transcript_id, group_id)
);

CREATE OR REPLACE FUNCTION retraction_audience_block_mutation() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF current_setting('app.audit_maintenance', true) = 'on' THEN
        RETURN COALESCE(OLD, NEW);
    END IF;
    IF TG_OP = 'DELETE' AND NOT EXISTS (SELECT 1 FROM groups WHERE id = OLD.group_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'transcript_retraction_audiences is append-only: % refused in '
        'retraction_audience_block_mutation. It records which collectives saw a deleted transcript, '
        'so a retracted link can resolve for them. Rows leave only with their collective. For '
        'sanctioned maintenance, SET LOCAL app.audit_maintenance = ''on'' in one transaction and '
        'record why (docs/database-invariants.md, retraction audience).', TG_OP;
END $$;

CREATE TRIGGER trg_retraction_audience_immutable
    BEFORE UPDATE OR DELETE ON transcript_retraction_audiences
    FOR EACH ROW EXECUTE FUNCTION retraction_audience_block_mutation();

-- The existing BEFORE DELETE trigger invokes this replacement before the
-- transcript's share rows cascade, including during account deletion.
CREATE OR REPLACE FUNCTION audit_transcript_retract() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    actor UUID;
BEGIN
    actor := NULLIF(current_setting('app.actor_id', true), '')::uuid;
    IF actor IS NULL THEN
        RAISE EXCEPTION 'governance audit requires app.actor_id (fail-closed): DELETE on transcripts blocked. '
            'Route the deletion through inTxAs (authenticated user) or inTxAsSystem; raw SQL and '
            'sanctioned ops must SET LOCAL app.actor_id — system actor '
            '00000000-0000-0000-0000-000000000000 (see docs/deletion-data-lifecycle-model.md §7).';
    END IF;
    INSERT INTO transcript_governance_events_audit
        (transcript_id, event_type, license_id, visibility, changed_by)
    VALUES (OLD.id, 'retracted', OLD.license_id, OLD.visibility, actor);
    IF OLD.visibility = 'shared' THEN
        INSERT INTO transcript_retraction_audiences (transcript_id, group_id)
        SELECT OLD.id, s.group_id
        FROM transcript_shares s
        WHERE s.transcript_id = OLD.id AND s.status = 'approved'
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN OLD;
END $$;

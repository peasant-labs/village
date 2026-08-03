CREATE TABLE annotations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_hash    VARCHAR(128) NOT NULL UNIQUE,
    owner_id        UUID REFERENCES users(id) ON DELETE CASCADE NOT NULL,
    target_kind     VARCHAR(30) NOT NULL,
    session_id      TEXT,
    entry_session_id TEXT,
    entry_index     INT,
    entry_end_index INT,
    annotation_id   TEXT,
    project_hash    TEXT,
    type_id         VARCHAR(100) NOT NULL,
    value           TEXT NOT NULL,
    is_primary      BOOLEAN NOT NULL DEFAULT false,
    confidence      DOUBLE PRECISION,
    reason          TEXT,
    annotator_name  TEXT,
    provenance      JSONB,
    created_at      TIMESTAMPTZ DEFAULT now(),
    updated_at      TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX idx_annotations_session ON annotations(session_id);
CREATE INDEX idx_annotations_owner ON annotations(owner_id);
CREATE INDEX idx_annotations_type ON annotations(type_id);

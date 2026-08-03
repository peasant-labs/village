-- Usage attestations: verified org members attest that their org used/referenced a transcript
CREATE TABLE attestations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transcript_id   UUID REFERENCES transcripts(id) ON DELETE CASCADE NOT NULL,
    attester_id     UUID REFERENCES users(id) ON DELETE CASCADE NOT NULL,
    org_login       VARCHAR(255) NOT NULL,
    attestation_type VARCHAR(50) NOT NULL CHECK (
        attestation_type IN ('used_in_training', 'referenced', 'evaluated', 'deployed')
    ),
    note            TEXT,
    created_at      TIMESTAMPTZ DEFAULT now(),
    UNIQUE (transcript_id, org_login, attestation_type),
    FOREIGN KEY (attester_id, org_login)
        REFERENCES user_github_orgs(user_id, org_login) ON DELETE CASCADE
);
CREATE INDEX idx_attestations_transcript ON attestations(transcript_id);
CREATE INDEX idx_attestations_org ON attestations(org_login);

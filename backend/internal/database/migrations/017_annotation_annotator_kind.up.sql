-- annotator_kind records the producer of an annotation (human, agent, rule).
-- The CLI annotation push wire format does not carry this field, so pushed
-- rows leave it NULL and the read path infers a kind from provenance.method.
-- Manual per-turn labels created on the village set it explicitly to 'human'.
ALTER TABLE annotations
    ADD COLUMN annotator_kind VARCHAR(10)
        CHECK (annotator_kind IN ('human', 'agent', 'rule'));

-- name: GetLatestRetractionVisibility :one
SELECT visibility
FROM transcript_governance_events_audit
WHERE transcript_id = $1 AND event_type = 'retracted'
ORDER BY seq DESC
LIMIT 1;

-- name: ListRetractionAudiences :many
SELECT group_id
FROM transcript_retraction_audiences
WHERE transcript_id = $1;

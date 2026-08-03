-- name: InsertCLISession :one
INSERT INTO cli_auth_sessions (oauth_state, cli_port, cli_state)
VALUES ($1, $2, $3)
RETURNING id;

-- name: GetCLISessionByState :one
SELECT * FROM cli_auth_sessions
WHERE oauth_state = $1;

-- name: UpdateCLISessionWithCode :exec
UPDATE cli_auth_sessions
SET exchange_code = $2,
    user_id = $3,
    username = $4
WHERE oauth_state = $1;

-- name: ExchangeCLISession :one
UPDATE cli_auth_sessions
SET exchanged_at = now()
WHERE exchange_code = $1
  AND cli_state = $2
  AND exchanged_at IS NULL
  AND created_at > now() - interval '5 minutes'
RETURNING *;

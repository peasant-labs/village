CREATE TABLE cli_auth_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    oauth_state TEXT NOT NULL UNIQUE,
    cli_port INTEGER NOT NULL,
    cli_state TEXT NOT NULL,
    exchange_code TEXT UNIQUE,
    user_id UUID,
    username TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    exchanged_at TIMESTAMPTZ
);

CREATE INDEX idx_cli_auth_sessions_exchange_code
    ON cli_auth_sessions(exchange_code) WHERE exchange_code IS NOT NULL;

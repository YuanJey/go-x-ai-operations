CREATE TABLE IF NOT EXISTS x_rate_limits (
    account_id TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    limit_value INTEGER,
    remaining INTEGER,
    reset_at TEXT,
    observed_at TEXT NOT NULL,
    PRIMARY KEY (account_id, endpoint),
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS alerts (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    dedupe_key TEXT NOT NULL,
    severity TEXT NOT NULL,
    kind TEXT NOT NULL,
    message TEXT NOT NULL,
    details_json TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'open',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    resolved_at TEXT,
    UNIQUE(account_id, dedupe_key),
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE INDEX IF NOT EXISTS idx_alerts_open
ON alerts(account_id, state, severity, updated_at DESC);

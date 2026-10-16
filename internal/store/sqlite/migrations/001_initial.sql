CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    handle TEXT NOT NULL,
    timezone TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'active',
    pause_reason TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS credential_refs (
    id TEXT PRIMARY KEY,
    account_id TEXT,
    kind TEXT NOT NULL,
    item_ref TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS provider_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    protocol TEXT NOT NULL,
    base_url TEXT NOT NULL,
    model TEXT NOT NULL,
    location TEXT NOT NULL,
    capabilities_json TEXT NOT NULL,
    api_key_ref TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS model_catalogs (
    id TEXT PRIMARY KEY,
    provider_profile_id TEXT NOT NULL,
    model TEXT NOT NULL,
    capabilities_json TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    UNIQUE(provider_profile_id, model),
    FOREIGN KEY (provider_profile_id) REFERENCES provider_profiles(id)
);

CREATE TABLE IF NOT EXISTS rule_versions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    state TEXT NOT NULL,
    source_yaml TEXT NOT NULL,
    normalized_json TEXT NOT NULL,
    config_hash TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    effective_at TEXT NOT NULL,
    activated_at TEXT,
    created_at TEXT NOT NULL,
    UNIQUE(account_id, version),
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_rule_versions_one_active
ON rule_versions(account_id) WHERE state = 'active';

CREATE TABLE IF NOT EXISTS action_counters (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    rule_version_id TEXT NOT NULL,
    action TEXT NOT NULL,
    period_kind TEXT NOT NULL,
    period_start TEXT NOT NULL,
    count INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    UNIQUE(account_id, action, period_start, period_kind),
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (rule_version_id) REFERENCES rule_versions(id)
);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    action TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    payload_ref TEXT,
    rule_version_id TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    run_at TEXT NOT NULL,
    deadline_at TEXT,
    priority INTEGER NOT NULL DEFAULT 0,
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 1,
    lease_owner TEXT,
    lease_until TEXT,
    state TEXT NOT NULL DEFAULT 'pending',
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (rule_version_id) REFERENCES rule_versions(id)
);

CREATE INDEX IF NOT EXISTS idx_jobs_lease
ON jobs(state, run_at, priority DESC, created_at);

CREATE TABLE IF NOT EXISTS outbox_events (
    event_id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TEXT NOT NULL,
    lease_owner TEXT,
    lease_until TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    published_at TEXT,
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE INDEX IF NOT EXISTS idx_outbox_dispatch
ON outbox_events(state, available_at, created_at);

CREATE TABLE IF NOT EXISTS sources (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    allow_pattern TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS source_items (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    url TEXT NOT NULL,
    author TEXT,
    published_at TEXT,
    title TEXT,
    summary TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    fetched_at TEXT NOT NULL,
    license_type TEXT,
    UNIQUE(source_id, url, content_hash),
    FOREIGN KEY (source_id) REFERENCES sources(id)
);

CREATE TABLE IF NOT EXISTS strategy_versions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    state TEXT NOT NULL,
    strategy_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(account_id, version),
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS topics (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    strategy_version_id TEXT,
    title TEXT NOT NULL,
    pillar TEXT NOT NULL,
    score REAL NOT NULL,
    rationale TEXT NOT NULL,
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions(id)
);

CREATE TABLE IF NOT EXISTS content_items (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    topic_id TEXT,
    strategy_version_id TEXT,
    rule_version_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    body TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    language TEXT NOT NULL,
    state TEXT NOT NULL,
    approval_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (topic_id) REFERENCES topics(id),
    FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions(id),
    FOREIGN KEY (rule_version_id) REFERENCES rule_versions(id)
);

CREATE INDEX IF NOT EXISTS idx_content_queue
ON content_items(account_id, kind, state, created_at);

CREATE TABLE IF NOT EXISTS content_sources (
    content_id TEXT NOT NULL,
    source_item_id TEXT NOT NULL,
    claim TEXT,
    PRIMARY KEY (content_id, source_item_id),
    FOREIGN KEY (content_id) REFERENCES content_items(id),
    FOREIGN KEY (source_item_id) REFERENCES source_items(id)
);

CREATE TABLE IF NOT EXISTS mentions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    x_post_id TEXT NOT NULL,
    author_id TEXT NOT NULL,
    author_handle TEXT,
    conversation_id TEXT,
    text TEXT NOT NULL,
    participant_ids_json TEXT NOT NULL,
    summoned INTEGER NOT NULL,
    summon_evidence TEXT,
    received_at TEXT NOT NULL,
    state TEXT NOT NULL,
    UNIQUE(account_id, x_post_id),
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS reply_decisions (
    id TEXT PRIMARY KEY,
    mention_id TEXT NOT NULL UNIQUE,
    decision TEXT NOT NULL,
    reason TEXT NOT NULL,
    risk_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (mention_id) REFERENCES mentions(id)
);

CREATE TABLE IF NOT EXISTS replies (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    mention_id TEXT NOT NULL UNIQUE,
    content_id TEXT NOT NULL,
    x_post_id TEXT,
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (mention_id) REFERENCES mentions(id),
    FOREIGN KEY (content_id) REFERENCES content_items(id)
);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    rule_version_id TEXT NOT NULL,
    strategy_version_id TEXT,
    state TEXT NOT NULL,
    input_json TEXT NOT NULL,
    output_json TEXT,
    error TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (rule_version_id) REFERENCES rule_versions(id),
    FOREIGN KEY (strategy_version_id) REFERENCES strategy_versions(id)
);

CREATE TABLE IF NOT EXISTS agent_runs (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    input_schema_version TEXT NOT NULL,
    output_schema_version TEXT NOT NULL,
    provider_profile TEXT,
    provider_protocol TEXT,
    model TEXT,
    input_json TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    output_json TEXT,
    output_hash TEXT,
    status TEXT NOT NULL,
    error TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    FOREIGN KEY (workflow_id) REFERENCES workflow_runs(id)
);

CREATE TABLE IF NOT EXISTS llm_usage (
    id TEXT PRIMARY KEY,
    provider_profile_id TEXT,
    agent_run_id TEXT,
    model TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    estimated_cost REAL NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'USD',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    FOREIGN KEY (provider_profile_id) REFERENCES provider_profiles(id),
    FOREIGN KEY (agent_run_id) REFERENCES agent_runs(id)
);

CREATE TABLE IF NOT EXISTS api_operations (
    operation_id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    action TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    request_json TEXT NOT NULL,
    state TEXT NOT NULL,
    x_post_id TEXT,
    response_json TEXT,
    error_code TEXT,
    error_message TEXT,
    prepared_at TEXT NOT NULL,
    sent_at TEXT,
    resolved_at TEXT,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE INDEX IF NOT EXISTS idx_api_operations_reconcile
ON api_operations(account_id, state, updated_at);

CREATE TABLE IF NOT EXISTS x_posts (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    content_id TEXT NOT NULL,
    operation_id TEXT NOT NULL UNIQUE,
    x_post_id TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    parent_x_post_id TEXT,
    body_hash TEXT NOT NULL,
    published_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (content_id) REFERENCES content_items(id),
    FOREIGN KEY (operation_id) REFERENCES api_operations(operation_id)
);

CREATE TABLE IF NOT EXISTS metric_snapshots (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    metrics_json TEXT NOT NULL,
    captured_at TEXT NOT NULL,
    UNIQUE(account_id, subject_type, subject_id, captured_at),
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS experiments (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    hypothesis TEXT NOT NULL,
    config_json TEXT NOT NULL,
    state TEXT NOT NULL,
    result_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE TABLE IF NOT EXISTS pricing_catalogs (
    id TEXT PRIMARY KEY,
    version TEXT NOT NULL UNIQUE,
    currency TEXT NOT NULL,
    catalog_json TEXT NOT NULL,
    effective_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cost_ledger (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    operation_id TEXT,
    provider_profile_id TEXT,
    resource_type TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    units REAL NOT NULL,
    estimated_cost REAL NOT NULL,
    actual_cost REAL,
    currency TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id),
    FOREIGN KEY (operation_id) REFERENCES api_operations(operation_id),
    FOREIGN KEY (provider_profile_id) REFERENCES provider_profiles(id)
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    details_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY (account_id) REFERENCES accounts(id)
);

CREATE INDEX IF NOT EXISTS idx_audit_account_time
ON audit_logs(account_id, created_at DESC);

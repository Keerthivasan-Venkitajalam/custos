-- Idempotent by design: this file is both the local docker-entrypoint-initdb.d
-- seed (runs once on an empty volume) and the deployed gateway's own
-- startup self-migration (runs on every boot — see gateway.go's runMigrations).
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS mandates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         TEXT NOT NULL,
    total_blocked   NUMERIC(12,2) NOT NULL CHECK (total_blocked > 0),
    utilized        NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (utilized >= 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT within_mandate CHECK (utilized <= total_blocked)
);

CREATE TABLE IF NOT EXISTS transactions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mandate_id      UUID NOT NULL REFERENCES mandates(id),
    amount          NUMERIC(12,2) NOT NULL,
    recipient_vpa   TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('EXECUTED','REJECTED_UNSIGNED','REJECTED_INVALID_SIG','REJECTED_REPLAY','REJECTED_EXPIRED','REJECTED_INSUFFICIENT_FUNDS')),
    jwt_jti         TEXT UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_log (
    id              BIGSERIAL PRIMARY KEY,
    transaction_id  UUID REFERENCES transactions(id),
    raw_prompt      TEXT NOT NULL,
    tool_call_json  JSONB NOT NULL,
    enclave_verdict TEXT NOT NULL CHECK (enclave_verdict IN ('APPROVED','REJECTED')),
    reason          TEXT NOT NULL,
    latency_ms      INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
REVOKE UPDATE, DELETE ON audit_log FROM PUBLIC;

-- Seed mandate used by the demo/eval scripts
INSERT INTO mandates (id, user_id, total_blocked, utilized)
VALUES ('11111111-1111-1111-1111-111111111111', 'demo-user', 10000.00, 0)
ON CONFLICT (id) DO NOTHING;

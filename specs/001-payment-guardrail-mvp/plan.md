# Plan 001: Custos — Technical Architecture

Implements: [spec.md](spec.md) · Governed by: [../../CONSTITUTION.md](../../CONSTITUTION.md)

## 1. Architecture Overview — Three Planes

```
┌─────────────────────┐      tool call (untrusted)      ┌──────────────────────┐
│   Agentic Plane      │ ───────────────────────────────▶│   Enclave Plane       │
│  Flutter Web chat UI │                                  │  AWS Nitro Enclave    │
│  FastAPI orchestrator│◀──── signed JWT or rejection ────│  Rust policy engine   │
│  + LLM tool-calling  │                                  │  + intent classifier  │
└─────────────────────┘                                  └──────────┬────────────┘
                                                                      │ x-custos-proof-of-guardrail JWT
                                                                      ▼
                                                          ┌──────────────────────┐
                                                          │   Ledger Plane        │
                                                          │  Go mock gateway      │
                                                          │  verifies JWT (KMS)   │
                                                          │  Postgres ledger      │
                                                          └──────────────────────┘
```

The **only** thing that crosses from Enclave Plane to Ledger Plane is a signed JWT or nothing.
The gateway never sees the raw prompt, the tool call, or any agent reasoning — only a
signature it can verify against KMS. This is what makes Constitution §2 structurally true
rather than aspirationally true.

## 2. Component Breakdown & Stack

| Component | Stack | Responsibility |
|---|---|---|
| Frontend | Flutter Web | Chat UI + split-screen "X-Ray" attestation viewer |
| Orchestrator | Python, FastAPI, hosted LLM tool-calling API | Conversation, tool-call generation, routes payloads to enclave, never authorizes payment itself |
| Enclave service | Rust (policy engine) + quantized DistilRoBERTa via ONNX (intent check) | Runs **inside** an AWS Nitro Enclave; validates mandate balance, checks semantic match, signs via KMS |
| Gateway | Go | Deterministic state machine; verifies JWT signature only; talks to Postgres |
| Ledger DB | PostgreSQL | `mandates`, `transactions`, `audit_log` (append-only) |
| Observability | Prometheus + Grafana | Latency, rejection rate, injection-attempt volume |

## 3. Data Model (PostgreSQL)

```sql
-- Blocked-fund mandate (simulated UPI Reserve Pay)
CREATE TABLE mandates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         TEXT NOT NULL,
    total_blocked   NUMERIC(12,2) NOT NULL CHECK (total_blocked > 0),
    utilized        NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (utilized >= 0),
    remaining       NUMERIC(12,2) GENERATED ALWAYS AS (total_blocked - utilized) STORED,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT within_mandate CHECK (utilized <= total_blocked)
);

-- Every debit attempt, signed or not
CREATE TABLE transactions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mandate_id      UUID NOT NULL REFERENCES mandates(id),
    amount          NUMERIC(12,2) NOT NULL,
    recipient_vpa   TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('EXECUTED','REJECTED_UNSIGNED','REJECTED_INVALID_SIG','REJECTED_REPLAY')),
    jwt_jti         TEXT,                       -- nonce, for replay detection
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only, never UPDATE or DELETE
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    transaction_id  UUID REFERENCES transactions(id),
    raw_prompt      TEXT NOT NULL,               -- sanitized before insert (Constitution §8)
    tool_call_json  JSONB NOT NULL,
    enclave_verdict TEXT NOT NULL CHECK (enclave_verdict IN ('APPROVED','REJECTED')),
    reason          TEXT NOT NULL,
    pcr_hash        TEXT,                        -- enclave measurement at signing time
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Enforce append-only at the DB layer, not just convention:
REVOKE UPDATE, DELETE ON audit_log FROM PUBLIC;
```

Row-level locking (`SELECT ... FOR UPDATE`) on `mandates` during debit to prevent race
conditions from concurrent tool calls.

## 4. API Contracts

### 4.1 Orchestrator → Enclave (over VSOCK)

```json
{
  "raw_prompt": "string, sanitized",
  "agent_scratchpad": "string, chain-of-thought",
  "tool_call": {
    "name": "execute_upi_reserve_debit",
    "amount": 450.00,
    "recipient_vpa": "merchant@upi",
    "mandate_id": "uuid"
  },
  "nonce": "uuid",
  "timestamp": "iso8601"
}
```

Response (approved):

```json
{
  "verdict": "APPROVED",
  "signed_jwt": "eyJ...",
  "pcr_hash": "sha384:...",
  "latency_ms": 87
}
```

Response (rejected) — no signature is ever produced on rejection:

```json
{
  "verdict": "REJECTED",
  "reason": "semantic_mismatch | mandate_exceeded | schema_violation",
  "latency_ms": 41
}
```

### 4.2 JWT Claims (`x-custos-proof-of-guardrail`)

```json
{
  "mandate_id": "uuid",
  "amount": 450.00,
  "recipient_vpa": "merchant@upi",
  "jti": "uuid",          // nonce — gateway rejects reused jti
  "iat": 1234567890,
  "exp": 1234567895       // iat + 5s, hard TTL (Constitution: no long-lived proofs)
}
```

**Correction (found during T-16/T-17 implementation):** KMS's attestation/PCR-gating mechanism
(the `Recipient` parameter + `kms:RecipientAttestation:PCR0` condition keys) only applies to
`Decrypt`, `GenerateDataKey`, `GenerateDataKeyPair`, and `GenerateRandom` — **`Sign` has no
`Recipient` field and cannot be PCR-gated directly.** The originally-stated design ("KMS key
policy requires PCR match to sign") isn't achievable as an attestation-gated `Sign` call.

Actual mechanism, same end guarantee: a KMS **symmetric** CMK encrypts the enclave's RSA
signing private key at rest (as ciphertext, harmless without the CMK). The enclave calls
`kms:Decrypt` on that ciphertext, presenting its attestation document via `Recipient` — KMS's
key policy condition on `kms:RecipientAttestation:PCR0` only lets the decrypted key be
re-encrypted back to (and thus readable by) an enclave whose PCR0 matches. The enclave decrypts
the private key **in memory only**, signs the JWT locally (e.g. Rust `jsonwebtoken` crate), and
never persists or exports the key material. Only this exact attested enclave image can ever
produce a valid signature — same guarantee as originally stated, correct AWS mechanics.

The Go gateway still verifies the JWT signature via the RSA public key (unrelated to how the
private key was obtained) + checks `exp` + checks `jti` against a short-lived seen-nonce cache.
Any failure → `REJECTED_*` status, no execution.

## 5. Failure Mode Matrix

| Failure | Behavior | Constitution ref |
|---|---|---|
| Enclave unreachable/timeout | Gateway sees no signature → reject. Orchestrator surfaces "payments unavailable," keeps chat/browsing alive | §3 |
| KMS unreachable from enclave | Enclave cannot sign → returns REJECTED, same as above | §3 |
| JWT expired (>5s old) | Gateway rejects — prevents replay of a stale approval | §2 |
| JWT `jti` reused | Gateway rejects — prevents replay of a valid approval against a second debit | §2 |
| Agent retries a rejected call | Hard cap of 2 retries in orchestrator, then graceful fallback message — prevents retry storms against the enclave | Ops concern, see tasks.md T-11 |
| Enclave classifier disagrees with deterministic policy engine | Deterministic policy engine is authoritative; classifier is advisory-only inside the enclave (defense in depth, not a second veto that can be gamed) | §2 |

## 6. Local Development Without AWS

Real Nitro Enclaves require a Nitro-based EC2 instance with "enclave options" enabled at
launch — not free-tier, costs real money, and isn't available to develop against locally.
This does **not** require a bare-metal (`.metal`) instance despite earlier AWS docs implying
that; current-gen instance types (e.g. `m5.xlarge`, `c5.xlarge`) support enclaves directly at
roughly 1/20th the hourly cost of `.metal` — verify current supported types against AWS's docs
before provisioning, since this changes over time. To keep iteration fast:

- Build the enclave logic (`policy_engine.rs`) as a **plain binary first**, unit-testable
  outside any enclave.
- Stand up a local **KMS stub** (a tiny signing service in an isolated Docker container, no
  network egress) that mimics the sign/verify contract exactly.
- Only the final integration (Day 7+ in tasks.md) needs the real EC2 Nitro instance, and only
  for a bounded window — spin up, validate, tear down, to control cost.
- **Action needed from you before Day 7:** confirm AWS account + billing access exists, since
  this is the one dependency that can't be simulated away.

## 7. Repository Layout

```
custos/
├── specs/001-payment-guardrail-mvp/   # this spec, plan, tasks
├── agent_orchestrator/                # FastAPI + LLM tool-calling
│   ├── main.py
│   ├── agents/
│   └── tools/
├── enclave_service/                   # runs inside Nitro Enclave
│   ├── policy_engine.rs
│   ├── model/                         # quantized DistilRoBERTa (ONNX)
│   └── build_eif.sh
├── ledger_gateway/                    # Go mock payment gateway
│   ├── gateway.go
│   └── database/                      # SQL migrations
├── frontend/                          # Flutter Web
│   ├── lib/chat_ui/
│   └── lib/attestation_dashboard/
├── adversarial_eval/                  # injection payload test suite
├── docs/
│   ├── ARCHITECTURE.md
│   └── THREAT_MODEL.md                # STRIDE
├── docker-compose.yml
└── README.md
```

## 8. Observability

Prometheus metrics exported by orchestrator + gateway:

- `custos_attestation_latency_ms` (histogram)
- `custos_injection_attempts_total` (counter, by detection reason)
- `custos_rejection_rate` (gauge)
- `custos_tool_call_retry_total` (counter)

Grafana dashboard: latency p50/p95/p99, rejection rate over time, live feed of last N verdicts
— this doubles as the on-screen "X-Ray" panel for the demo.

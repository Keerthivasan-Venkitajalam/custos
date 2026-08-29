# Custos

Cryptographic Proof-of-Guardrail for Agentic Payments — a security interceptor that makes it
structurally impossible for a prompt-injected AI shopping agent to authorize an unapproved
payment, by requiring an isolated hardware enclave's cryptographic signature before any
transaction can execute.

## Spec-Driven Development

This project is built spec-first. Read in this order:

1. [`CONSTITUTION.md`](CONSTITUTION.md) — non-negotiable principles that constrain every
   decision below. Read this first; everything else is checked against it.
2. [`specs/001-payment-guardrail-mvp/spec.md`](specs/001-payment-guardrail-mvp/spec.md) — what
   the system must do and why, as user stories with acceptance criteria. No implementation
   detail.
3. [`specs/001-payment-guardrail-mvp/plan.md`](specs/001-payment-guardrail-mvp/plan.md) — the
   technical architecture, stack, data model, and API contracts that satisfy the spec.
4. [`specs/001-payment-guardrail-mvp/tasks.md`](specs/001-payment-guardrail-mvp/tasks.md) — the
   dependency-ordered, independently-verifiable build sequence. Work through this top to bottom.

If you're picking this up to start building: skim the constitution, skim the spec's user
stories, then start at T-01 in tasks.md. Each task has a checkable Definition of Done — don't
mark it done until the DoD is actually true, not just "looks right."

## Quickstart (local, no AWS needed)

Everything below runs on your machine with the enclave stubbed by a local RSA keypair
(plan.md §6) — no cloud cost, no AWS account required for this part.

```bash
# 1. Postgres (schema + seed mandate load automatically from ledger_gateway/migrations/)
docker compose up -d

# 2. Local KMS-stub keypair (one-time; already generated if keystore/*.pem exist)
openssl genrsa -out keystore/private_key.pem 2048
openssl rsa -in keystore/private_key.pem -pubout -out keystore/public_key.pem

# 3. Gateway (Go) — reject-by-default payment executor
cd ledger_gateway
DATABASE_URL="postgresql://custos:custos@localhost:5433/custos?sslmode=disable" \
JWT_PUBLIC_KEY_PATH="../keystore/public_key.pem" \
go run gateway.go &

# 4. Enclave stub (Python) — deterministic policy engine + JWT signer
cd ../enclave_service
python3 -m venv ../.venv-enclave && ../.venv-enclave/bin/pip install -r requirements.txt
PRIVATE_KEY_PATH="../keystore/private_key.pem" \
DATABASE_URL="postgresql://custos:custos@localhost:5433/custos" \
../.venv-enclave/bin/uvicorn policy_engine:app --port 8100 &

# 5. Orchestrator (FastAPI + Groq tool-calling agent)
cd ../agent_orchestrator
python3 -m venv ../.venv-orch && ../.venv-orch/bin/pip install -r requirements.txt
set -a && source ../.env && set +a
../.venv-orch/bin/uvicorn main:app --port 8000 &

# 6. Frontend (chat UI + live X-Ray attestation panel)
cd ../frontend && python3 -m http.server 8080 &
```

Open `http://localhost:8080` — use the "Load benign request" / "Load injection attack" preset
buttons to see a legitimate purchase get signed and executed side by side with an indirect
prompt injection getting refused before it ever reaches the gateway.

Adversarial eval: `cd adversarial_eval && ../.venv-orch/bin/python run_eval.py` (needs the
`psycopg[binary]` package installed in that venv too).

## Real AWS Nitro Enclave

`enclave_service/nitro_poc/` holds the real deployment target, verified end to end against
actual Nitro Enclave hardware — not `.metal`-family required, a regular Nitro-capable instance
(e.g. `c6g.xlarge`) with the enclave option enabled works and is far cheaper:

- `vsock_echo_server.py` / `vsock_ping_client.py` — minimal VSOCK connectivity proof.
- `custos_enclave_app.py` — the real policy engine: decrypts its RSA signing key via
  attestation-gated `kms:Decrypt` (using AWS's own `kmstool_enclave_cli`, not a hand-rolled
  crypto flow), holds it in memory only, and signs approved payloads locally.
- `parent_bootstrap.sh` — runs on the EC2 host (not inside the enclave); relays IMDS
  credentials and the sealed key into the enclave over VSOCK, since the enclave has no network
  access of its own.
- `vsock_http_bridge.py` — runs on the EC2 host; exposes the same `POST /guardrail/evaluate`
  contract as the local stub, so the orchestrator's code is identical whether it's talking to
  the local stub or the real enclave — only the tunnel target changes.

See `specs/001-payment-guardrail-mvp/tasks.md` (T-16–T-18) for the full verification trail,
including the real PCR measurements and the false-positive bug that testing against the real
enclave surfaced (and how it was fixed) in the in-memory mandate ledger design.

<div align="center">

<img src="public/logo-icon.png" width="100" alt="Custos" />

# Custos

### A Cryptographic Guardrail for Agentic Payments

**The LLM proposes. It never decides. A separate, isolated policy engine checks every payment against the mandate and the user's own words — and only its signature can move money.**

<br/>

[![Live Product](https://img.shields.io/badge/🔴%20LIVE-custos--amber.vercel.app-3268F5?style=for-the-badge)](https://custos-amber.vercel.app)
[![Video Demo](https://img.shields.io/badge/▶%20Demo%20Video-YouTube-FF0000?style=for-the-badge&logo=youtube)](https://youtu.be/iHrx7Pvj7YM)
[![How It Works](https://img.shields.io/badge/📖%20How%20It%20Works-Read%20the%20docs-8B939F?style=for-the-badge)](https://custos-amber.vercel.app/docs.html)

[![Python](https://img.shields.io/badge/Python-3.12-3776AB?style=flat-square&logo=python&logoColor=white)](https://python.org)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![AWS Nitro Enclaves](https://img.shields.io/badge/AWS-Nitro%20Enclaves-FF9900?style=flat-square&logo=amazonaws&logoColor=white)](https://aws.amazon.com/ec2/nitro/nitro-enclaves/)
[![Vercel](https://img.shields.io/badge/Frontend-Vercel-000000?style=flat-square&logo=vercel)](https://vercel.com)
[![Render](https://img.shields.io/badge/Services-Render-46E3B7?style=flat-square&logo=render&logoColor=white)](https://render.com)
[![Neon](https://img.shields.io/badge/Postgres-Neon-00E599?style=flat-square&logo=postgresql&logoColor=white)](https://neon.tech)

<br/>

[The Problem](#the-problem) · [Live Guardrail Runs](#live-guardrail-runs) · [Architecture](#system-architecture) · [Real AWS Verification](#real-aws-nitro-enclave-verification) · [Getting Started](#getting-started) · [Spec-Driven Docs](#spec-driven-development)

<br/>

![Custos Guardrail Attestation](public/screenshots/03-injection-rejected.png)

</div>

---

## Deliverables

> Everything needed to evaluate this, one table, zero hunting.

| Artifact | Link | Description |
| :--- | :--- | :--- |
| **Live Product** | [custos-amber.vercel.app](https://custos-amber.vercel.app) | Chat with the agent, watch an injection attempt get blocked live |
| **Video Demo** | [youtu.be/iHrx7Pvj7YM](https://youtu.be/iHrx7Pvj7YM) | Narrated walkthrough — problem, architecture, live demo, real AWS proof |
| **GitHub** | [Keerthivasan-Venkitajalam/custos](https://github.com/Keerthivasan-Venkitajalam/custos) | Full source, public |
| **How It Works** | [/docs.html](https://custos-amber.vercel.app/docs.html) | Architecture, real AWS evidence, what's real vs. simulated in the hosted build |
| **Constitution** | [`CONSTITUTION.md`](CONSTITUTION.md) | The non-negotiable principles every decision here is checked against |
| **Spec / Plan / Tasks** | [`specs/001-payment-guardrail-mvp/`](specs/001-payment-guardrail-mvp/) | What it does, why, the architecture, and the full build + verification log |
| **Adversarial Eval** | [`adversarial_eval/`](adversarial_eval/) | The actual injection payloads and the script that runs them end to end |

> **For reviewers:** the live product talks to real backend services (Render free tier — first request after idle can take 30–60s to wake up), not a mockup.

---

## The Problem

AI agents are starting to hold real financial authority — pre-authorized payment mandates that let them pay without a PIN on every transaction. That convenience is also a new attack surface: **indirect prompt injection**, currently rated the number one security risk for agentic AI systems. If an agent reads a poisoned product page, a fake support ticket, or a manipulated email, it can be talked into authorizing a payment its user never asked for.

Today, most systems defend against this with a system prompt — a polite instruction asking the model not to do that.

```
That is not a security boundary. That is a suggestion, and suggestions can be argued with.
```

**Custos replaces the suggestion with a hard boundary.** The LLM never has the authority to move money — it can only *propose* a payment. A separate, isolated policy engine independently checks that proposal against the mandate and against what the user actually said, and only signs a cryptographic proof if it's genuinely approved. The payment gateway trusts exactly one thing: that signature. Not the conversation. Not the agent's confidence. A signature, or nothing executes.

It doesn't matter if the model gets fooled — it was never the thing with authority to spend.

![Custos Landing](public/screenshots/01-empty-state.png)

---

## Live Guardrail Runs

Two real requests against the live deployed system, no mocking:

### Legitimate purchase — APPROVED

> User asks the agent to buy a ₹220 item and pay `merchant@upi`. The guardrail checks the amount against the remaining mandate balance and confirms the amount and recipient actually appear in what the user said, signs a JWT with a 5-second expiry, and the gateway executes the debit.

- Verdict: **APPROVED** · Reason: `approved` · Gateway: `EXECUTED`
- Signed JWT returned, mandate balance updates live

### Indirect prompt injection — REJECTED

> A "support ticket" the agent reads contains a hidden instruction claiming the payment was already approved and to process it without checking again. The agent **is fooled** — it generates the tool call exactly as the attacker wanted, targeting an amount and recipient the real user never mentioned. The guardrail catches it anyway.

- Verdict: **REJECTED** · Reason: `semantic_mismatch` · Gateway: `NOT CALLED (unsigned)`
- No signature is ever produced — the gateway never even sees a request

**The model was compromised. The money never moved.**

![Custos Injection Blocked](public/screenshots/03-injection-rejected.png)

---

## System Architecture

Custos runs as **three planes that never share code or memory.** A payment proposal has to cross all three before anything executes.

```mermaid
graph TB
    User[👤 User] -->|message| FE[💬 Chat UI]
    FE -->|POST /chat| Orch[🧠 Orchestrator<br/>FastAPI + LLM tool-calling]

    subgraph "🕸️ Agentic Plane — untrusted"
        Orch
    end

    Orch -->|proposed tool call| Guard[🛡️ Guardrail<br/>enclave_service]

    subgraph "🔒 Enclave Plane — the guardrail"
        Guard -->|check mandate balance| Check1{Within budget?}
        Guard -->|check user's own words| Check2{Amount/recipient<br/>actually said?}
        Check1 & Check2 -->|both pass| Sign[✍️ Sign JWT<br/>5s expiry]
        Check1 & Check2 -->|either fails| Reject[❌ REJECTED<br/>no signature produced]
    end

    Sign -->|x-custos-proof-of-guardrail| GW[⚖️ Gateway<br/>Go, deterministic]
    Reject -.->|nothing to verify| GW

    subgraph "📒 Ledger Plane — trusts one thing"
        GW -->|verify signature only| Valid{Valid sig?}
        Valid -->|yes| Execute[✅ Debit executed]
        Valid -->|no / none| Deny[🚫 Rejected before DB touched]
        Execute --> DB[(🗄️ Postgres<br/>row-locked, append-only audit)]
    end

    classDef untrusted fill:#E11D48,stroke:#9f0f34,stroke-width:2px,color:#fff;
    classDef guardrail fill:#3268F5,stroke:#1d45c9,stroke-width:2px,color:#fff;
    classDef ledger fill:#10B981,stroke:#0a7a56,stroke-width:2px,color:#fff;

    class Orch untrusted;
    class Guard,Check1,Check2,Sign,Reject guardrail;
    class GW,Valid,Execute,Deny,DB ledger;
```

### The Three Planes

| Plane | Directory | Trust Level | Responsibility |
| :---: | :--- | :--- | :--- |
| **Agentic** | [`agent_orchestrator/`](agent_orchestrator/) | Untrusted | Holds the conversation, generates a proposed tool call. Cannot execute a payment — there is exactly one call site to the gateway in the whole orchestrator, and it only fires after a signed approval comes back. |
| **Enclave** | [`enclave_service/`](enclave_service/) | The guardrail | Independently checks the proposal against the remaining mandate balance and against the user's own words. Signs a cryptographic proof only if genuinely approved. In the real AWS path, this runs inside actual Nitro Enclave hardware. |
| **Ledger** | [`ledger_gateway/`](ledger_gateway/) | Deterministic | A Go service that verifies the guardrail's signature and nothing else. No signature, no execution — every unsigned/expired/replayed request is rejected before the database is ever touched. |

---

## Real AWS Nitro Enclave Verification

This isn't a diagram of what a hardware enclave would do — it was built and verified against actual AWS Nitro Enclave hardware.

| Step | What Happened |
| :--- | :--- |
| **Host** | `c6g.xlarge` (Graviton, Nitro Enclave option enabled) — not the far pricier `.metal` family some older docs imply is required |
| **Enclave isolation** | No network interface of its own, no persistent storage |
| **Key sealing** | RSA signing key sealed with AWS KMS, released only via attestation-gated `kms:Decrypt` |
| **Attestation gate** | The enclave must cryptographically prove its own measured code identity (PCR0) before AWS will release the key — change one byte of the enclave's code and the proof fails |
| **Credential relay** | The enclave has no network path of its own, so the parent host relays IMDS credentials and the sealed ciphertext in over **VSOCK only** |
| **Real toolchain** | Uses AWS's own `kmstool_enclave_cli` (NSM attestation + KMS SDK, built from source), not a hand-rolled crypto flow |

**What was actually measured, not claimed:**

```
PCR0: a1220284834be97457cfffe5b949aad883d2e2e23f3201a8a9efae6730e8cd74e5a5d9e29bbc258ee250ef937574508d
✓ attestation-gated kms:Decrypt — SUCCESS
✓ signed a test payload inside the enclave
✓ signature independently verified outside the enclave, against the public key,
  using nothing the enclave itself produced
```

Full verification trail — including a real architectural correction found mid-build (KMS `Sign` has no attestation support, only `Decrypt` does — the design had to change), and a false-positive bug the real enclave's in-memory ledger surfaced during testing — is documented in [`specs/001-payment-guardrail-mvp/tasks.md`](specs/001-payment-guardrail-mvp/tasks.md) (search "T-16" through "T-18b").

---

## Measured Results

From the adversarial evaluation suite — real injection payloads with realistic, non-obvious attacker identities, run end to end against the live system:

| Metric | Result |
| :--- | :---: |
| **Injection payloads prevented** | **100%** |
| **False positives on legitimate purchases** | **0%** |
| **Guardrail signing latency** (post-boot) | **< 1ms** — pure local crypto after the one-time key decrypt, no further network round trip |

One payload genuinely fooled the LLM — it generated the malicious tool call exactly as the attacker intended. The guardrail caught it anyway, because the model was never the thing with authority to spend.

---

## What's Real vs. Simulated

| Real | Simulated / Permanently Out of Scope |
| :--- | :--- |
| LLM tool-calling agent, live API calls | No live banking/UPI network integration — mock gateway only, by design |
| Deterministic policy engine (mandate + semantic checks) | No custom-trained models — hosted LLM APIs only |
| RS256 JWT signing and verification | Enclave's mandate ledger is seeded once at boot, not live-synced to Postgres (documented limitation) |
| Reject-by-default payment gateway with replay protection | No merchant onboarding, multi-tenant auth, or account management |
| Row-locked Postgres ledger, append-only audit log | |
| Real AWS Nitro Enclave, attestation-gated KMS decrypt, in-memory signing | |
| Actually deployed — Vercel + Render + Neon, not just local | |

---

## Getting Started

### Prerequisites

- Go 1.26+, Python 3.12+, Docker
- An LLM API key with tool-calling support (built against Groq's OpenAI-compatible API)

### Local Quickstart (no AWS needed)

Everything below runs on your machine with the enclave stubbed by a local RSA keypair — no cloud cost, no AWS account required for this part.

```bash
# 1. Clone
git clone https://github.com/Keerthivasan-Venkitajalam/custos.git
cd custos

# 2. Postgres (schema + seed mandate load automatically)
docker compose up -d

# 3. Local KMS-stub keypair (one-time)
openssl genrsa -out keystore/private_key.pem 2048
openssl rsa -in keystore/private_key.pem -pubout -out keystore/public_key.pem

# 4. Gateway (Go) — reject-by-default payment executor
cd ledger_gateway
DATABASE_URL="postgresql://custos:custos@localhost:5433/custos?sslmode=disable" \
JWT_PUBLIC_KEY_PATH="../keystore/public_key.pem" \
go run gateway.go &

# 5. Enclave stub (Python) — deterministic policy engine + JWT signer
cd ../enclave_service
python3 -m venv ../.venv-enclave && ../.venv-enclave/bin/pip install -r requirements.txt
PRIVATE_KEY_PATH="../keystore/private_key.pem" \
DATABASE_URL="postgresql://custos:custos@localhost:5433/custos" \
../.venv-enclave/bin/uvicorn policy_engine:app --port 8100 &

# 6. Orchestrator (FastAPI + tool-calling agent)
cd ../agent_orchestrator
python3 -m venv ../.venv-orch && ../.venv-orch/bin/pip install -r requirements.txt
echo "GROQ_API_KEY=your_key_here" > ../.env
set -a && source ../.env && set +a
../.venv-orch/bin/uvicorn main:app --port 8000 &

# 7. Frontend (chat UI + live attestation panel)
cd ../frontend && python3 -m http.server 8080
```

Open `http://localhost:8080` — use the **"Load benign request"** / **"Load injection attempt"** preset buttons to see a legitimate purchase get signed and executed side by side with an indirect prompt injection getting refused before it ever reaches the gateway.

```bash
# Adversarial eval — real numbers, not claimed
cd adversarial_eval && ../.venv-orch/bin/python run_eval.py
```

---

## Project Structure

```text
custos/
├── CONSTITUTION.md                    # Non-negotiable principles — every decision checked against this
├── specs/001-payment-guardrail-mvp/
│   ├── spec.md                        # What it does and why — user stories, acceptance criteria
│   ├── plan.md                        # Architecture, data model, API contracts
│   └── tasks.md                       # Full build + verification log, including real bugs found & fixed
├── agent_orchestrator/                # FastAPI + LLM tool-calling (Agentic Plane)
├── enclave_service/
│   ├── policy_engine.py               # Local dev stub — deterministic checks + JWT signing
│   └── nitro_poc/                     # Real AWS Nitro Enclave app + VSOCK bridge + bootstrap
├── ledger_gateway/                    # Go — deterministic, reject-by-default gateway
│   └── migrations/                    # Postgres schema, self-migrating on boot
├── frontend/                          # Chat UI + live attestation viewer, deployed on Vercel
├── adversarial_eval/                  # Real injection payloads + the script that runs them
└── keystore/                          # Public key only — private keys are gitignored, never committed
```

---

## Spec-Driven Development

This project is built spec-first. Read in this order:

1. [`CONSTITUTION.md`](CONSTITUTION.md) — non-negotiable principles that constrain every decision below.
2. [`specs/001-payment-guardrail-mvp/spec.md`](specs/001-payment-guardrail-mvp/spec.md) — what the system must do and why, as user stories with acceptance criteria.
3. [`specs/001-payment-guardrail-mvp/plan.md`](specs/001-payment-guardrail-mvp/plan.md) — technical architecture, stack, data model, API contracts.
4. [`specs/001-payment-guardrail-mvp/tasks.md`](specs/001-payment-guardrail-mvp/tasks.md) — the dependency-ordered, independently-verifiable build sequence, with real evidence (not just checkmarks) for every completed task.

Each task has a checkable Definition of Done — nothing is marked done until the DoD is actually true, not just "looks right."

---

## Live Deployment

| Component | Platform | Notes |
| :--- | :--- | :--- |
| Frontend | [Vercel](https://vercel.com) | Static, auto-deploys on push to `main` |
| Orchestrator, Gateway, Enclave stub | [Render](https://render.com) | Three independent services, free tier |
| Ledger | [Neon](https://neon.tech) | Managed Postgres |

The hosted path runs the local *simulated* enclave (deterministic policy engine, RS256 signing) — the real AWS Nitro Enclave needs actual Nitro-capable hardware a serverless platform can't provide, so it's verified separately (see [Real AWS Nitro Enclave Verification](#real-aws-nitro-enclave-verification) above) rather than running live on the hosted site.

---

<div align="center">

**Built by [Keerthivasan S V](https://github.com/Keerthivasan-Venkitajalam)**

[Live Product](https://custos-amber.vercel.app) · [Video Demo](https://youtu.be/iHrx7Pvj7YM) · [Report an Issue](https://github.com/Keerthivasan-Venkitajalam/custos/issues)

*The model can be fooled. It still can't spend a rupee it wasn't supposed to.*

</div>

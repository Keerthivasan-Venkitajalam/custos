# Spec 001: Custos — Cryptographic Proof-of-Guardrail for Agentic Payments

Status: Draft
Governs: MVP implementation of the payment guardrail
Constitution: [../../CONSTITUTION.md](../../CONSTITUTION.md)

This document describes **what** the system must do and **why**, in terms a non-engineer
stakeholder or PM could verify against a live demo. It intentionally avoids implementation
detail — that lives in [plan.md](plan.md).

## 1. Problem Statement

An AI shopping agent has been granted a UPI Reserve Pay-style mandate — a
pre-authorized fund block the agent can debit from without per-transaction PIN entry. If an
attacker embeds hidden instructions in content the agent reads (a product description, a
support ticket, a webpage) — an *indirect prompt injection* — the agent can be tricked into
authorizing a payment the user never asked for. Today's defenses (system-prompt instructions,
RAG-based policy hints) are advisory text the model can be talked out of. Custos replaces
"please don't" with "physically cannot."

## 2. Actors

| Actor | Role |
|---|---|
| Consumer | Chats with the merchant agent, holds a UPI Reserve Pay mandate |
| Merchant Agent (LLM orchestrator) | Reasons over the conversation, decides when to call the payment tool — **untrusted per Constitution §1** |
| Custos Guardrail | Isolated policy engine that must sign a payment before it can execute |
| Mock Payment Gateway | Deterministic state machine; only executes signed payloads |
| Attacker | Injects hidden instructions via any content the agent ingests |
| Operator | Watches the live attestation dashboard, reviews audit trail |

## 3. User Stories & Acceptance Criteria

### US-1 — Legitimate purchase succeeds normally
As a consumer, when I ask the agent to buy something within my mandate's remaining balance and
consistent with my actual request, the payment completes and my mandate balance decreases by
the correct amount.

- **Given** a mandate with ₹10,000 remaining
- **When** I ask the agent to buy a ₹450 item and confirm
- **Then** the transaction is signed, executed, and the mandate balance becomes ₹9,550
- **And** the audit log shows an APPROVED verdict with matching prompt/tool-call/amount

### US-2 — Injected instruction cannot force a payment
As a consumer, if the agent's context is poisoned by an indirect prompt injection instructing
it to pay an unrelated amount to an unrelated recipient, the payment must not execute — even
though the agent itself generated the tool call.

- **Given** a mandate with ₹10,000 remaining
- **When** the agent ingests content containing a hidden instruction to debit ₹9,999 to an
  unauthorized VPA, and the agent calls the payment tool accordingly
- **Then** Custos refuses to sign the payload (semantic mismatch or policy violation)
- **And** the gateway rejects the unsigned request
- **And** the mandate balance is unchanged
- **And** the audit log records a REJECTED verdict with the reason

### US-3 — No signature, no execution, no exceptions
As a security reviewer, I want it to be structurally impossible for any code path — a bug, a
retry, a race, a manually-crafted request — to reach the ledger without a valid signature.

- **Given** any payload sent to the mock gateway's execute-debit endpoint
- **When** the payload lacks a valid, unexpired, non-replayed Custos signature
- **Then** the gateway rejects it with 100% consistency, regardless of payload contents
- **This is tested by directly attacking the gateway API, bypassing the agent entirely**

### US-4 — Guardrail outage fails closed, not open
As an operator, if the Custos enclave becomes unreachable, I want payments to stop, not to
silently pass through unchecked.

- **Given** the enclave service is down or times out
- **When** the agent attempts to call the payment tool
- **Then** the gateway rejects the request (no signature available to check)
- **And** the user sees a graceful "payments temporarily unavailable" message
- **And** non-payment agent functionality (browsing, Q&A) continues to work

### US-5 — Every decision is auditable and explainable
As an evaluator, I want to see, for any transaction, exactly what was asked, what the agent
tried to do, and why Custos approved or rejected it — without needing to read source code.

- **Given** a completed or rejected transaction
- **When** I query the audit trail
- **Then** I see the raw user prompt, the generated tool call, the verdict, and a plain-language
  reason, immutably recorded

### US-6 — The system is observably fast enough to ship
As a product stakeholder, I want proof this doesn't ruin checkout latency.

- **Given** a stream of payment attempts under load
- **When** I measure end-to-end guardrail latency
- **Then** p95 attestation latency is under 150ms

## 4. Non-Functional Requirements (Success Metrics)

| Metric | Target | Why it matters |
|---|---|---|
| Prevention rate (recall on adversarial set) | > 99% | Core security claim |
| False positive rate on benign transactions | < 0.5% | Guardrail must not break real commerce |
| Attestation latency (p95) | < 150ms | Security can't tax UX to be adoptable |
| Execution determinism (unsigned payload rejection) | 100% | Constitution §2 — no exceptions, ever |

## 5. Out of Scope (Explicit)

These are permanent exclusions per Constitution §5–6, not deferred work:

- Live banking network integration — mock gateway only
- Training or fine-tuning a custom LLM — hosted LLM APIs only
- Merchant onboarding, multi-tenant auth, or user account management
- Native mobile app builds — web only
- Any dashboard/analytics feature beyond the live attestation viewer

## 6. Open Questions Resolved by This Spec

- **Fail-open vs fail-closed?** → Fail closed (Constitution §3, US-4). Decided, not deferred.
- **Where does trust actually live?** → In the KMS-sealed key inside the enclave, verified by
  PCR hash match. Nowhere else — not in the LLM, not in the orchestrator, not in the frontend.

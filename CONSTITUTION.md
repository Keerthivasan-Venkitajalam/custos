# Custos — Project Constitution

Non-negotiable principles. Every spec, plan, and task in this repo must be checked against
these before being written, and every PR/review must be checked against these before merge.
If a proposed change violates a principle, the principle wins — change the proposal, not the rule.

## 1. Zero-Trust Agent
The LLM orchestrator is never a trust boundary. Its tool calls are treated as
**untrusted input**, identical in status to text typed by an anonymous attacker. No code path
may execute a financial state change on the LLM's say-so alone.

## 2. Deterministic Enforcement Over Probabilistic Trust
The payment gateway trusts exactly one thing: a cryptographic signature produced by the
enclave's sealed key. It never inspects, trusts, or branches on LLM-generated text, confidence
scores, or "the agent said it was fine." Enforcement is a signature check, not a heuristic.

## 3. Fail Closed, Not Fail Open
If the enclave, KMS, or attestation path is unreachable, degraded, or times out, **no payment
executes**. A broken guardrail must never silently become "no guardrail." The one exception:
this applies to the *payment execution path* only — read-only operations (browsing a catalog,
answering questions) must keep working during a guardrail outage, so an outage degrades
commerce to "look but don't buy," not a total outage.

## 4. Explainability & Immutable Audit
Every payment attempt — approved or rejected — is written to an append-only audit log
containing the raw prompt, the tool call, and the enclave's verification verdict. Nothing is
overwritten or deleted. If a judge, auditor, or regulator asks "why did this happen," the
answer must already be on disk.

## 5. No Real Money, No Real Rails
This system never calls a live banking network or a live production payment gateway API.
All payment execution targets a mock gateway built in this repo. This is a permanent
constraint, not a Day-1-only shortcut — building "real" integration is explicitly out of scope
forever, not just for the MVP.

## 6. Surgical MVP Scope
Cut anything not required to prove the core claim ("a compromised LLM cannot forge a payment
signature"). No auth system, no merchant onboarding, no dashboard analytics beyond the
attestation viewer, no native mobile build. If a feature doesn't make the core demo more true
or more visible, it doesn't get built.

## 7. Latency Is a Correctness Requirement, Not a Nice-to-Have
The guardrail must add <150ms p95 to the checkout path. A security control that destroys UX
gets bypassed by product teams in the real world — so a latency regression is treated as a bug
of the same severity as a security bypass, not a performance nit to fix later.

## 8. Secrets Hygiene
No static AWS credentials in code or config — IAM roles only. No PII, prompts, or secrets in
logs without redaction. `.gitignore` covers `.env`, `*.pem`, `*.key` from commit 1, not added
reactively after a leak.

## Precedence
When a task in `tasks.md` conflicts with this document, this document wins. Update the task,
not the constitution — and if the constitution itself seems wrong, that's a conversation to
have explicitly, not a reason to quietly drift from it.

"""
Custos policy engine — the local stand-in for the AWS Nitro Enclave described
in plan.md §6. Same contract as the real thing: receives an untrusted tool
call, applies deterministic policy, and either signs it or refuses.

This process is deliberately the ONLY thing on the machine holding the
private signing key. The orchestrator never sees it. The gateway never sees
it, only the public key. That separation is the point, not an accident.
"""
import os
import time
import uuid

import jwt as pyjwt
import psycopg
from fastapi import FastAPI
from pydantic import BaseModel

PRIVATE_KEY_PATH = os.environ.get("PRIVATE_KEY_PATH", "/keystore/private_key.pem")
DATABASE_URL = os.environ.get(
    "DATABASE_URL", "postgresql://custos:custos@localhost:5433/custos"
)
JWT_TTL_SECONDS = 5

# PRIVATE_KEY_PEM (raw PEM content, e.g. a hosting provider's secret env var)
# takes priority over a file path — deployed environments shouldn't need a
# key file checked into a predictable path, and the real key never should be
# checked into the repo at all.
_private_key_pem = os.environ.get("PRIVATE_KEY_PEM")
if _private_key_pem:
    PRIVATE_KEY = _private_key_pem.encode()
else:
    with open(PRIVATE_KEY_PATH, "rb") as f:
        PRIVATE_KEY = f.read()

app = FastAPI(title="custos-enclave-stub")


class ToolCall(BaseModel):
    name: str
    mandate_id: str
    amount: float
    recipient_vpa: str


class GuardrailRequest(BaseModel):
    raw_prompt: str
    tool_call: ToolCall
    nonce: str | None = None


def get_mandate_remaining(mandate_id: str) -> float | None:
    conn = psycopg.connect(DATABASE_URL)
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT total_blocked - utilized FROM mandates WHERE id = %s",
                (mandate_id,),
            )
            row = cur.fetchone()
            return float(row[0]) if row else None
    finally:
        conn.close()


# Deterministic policy layer (Constitution §2: this is the load-bearing
# check; a semantic/ML classifier, if added later per tasks.md T-15, is
# advisory-only and can never override this).
def deterministic_check(req: GuardrailRequest) -> tuple[bool, str]:
    if req.tool_call.name != "execute_upi_reserve_debit":
        return False, "unknown_tool"
    if req.tool_call.amount <= 0:
        return False, "invalid_amount"

    remaining = get_mandate_remaining(req.tool_call.mandate_id)
    if remaining is None:
        return False, "unknown_mandate"
    if req.tool_call.amount > remaining:
        return False, "mandate_exceeded"

    # Semantic-mismatch heuristic: refuse to sign a payment whose amount and
    # recipient were never actually mentioned anywhere in the raw prompt the
    # user typed. This is intentionally crude (string containment) — it
    # exists to catch the class of attack in spec.md US-2, where the tool
    # call is well-formed and within budget, but the amount/recipient came
    # from injected content the user never referenced. It is exactly the
    # kind of rule tasks.md T-15's classifier is meant to strengthen later.
    amount_str = f"{req.tool_call.amount:.0f}"
    amount_str_2dp = f"{req.tool_call.amount:.2f}"
    mentions_amount = amount_str in req.raw_prompt or amount_str_2dp in req.raw_prompt
    mentions_recipient = req.tool_call.recipient_vpa in req.raw_prompt

    if not mentions_amount and not mentions_recipient:
        return False, "semantic_mismatch"

    return True, "approved"


def sign_payload(tool_call: ToolCall) -> str:
    now = int(time.time())
    claims = {
        "mandate_id": tool_call.mandate_id,
        "amount": tool_call.amount,
        "recipient_vpa": tool_call.recipient_vpa,
        "jti": str(uuid.uuid4()),
        "iat": now,
        "exp": now + JWT_TTL_SECONDS,
    }
    return pyjwt.encode(claims, PRIVATE_KEY, algorithm="RS256")


@app.post("/guardrail/evaluate")
def evaluate(req: GuardrailRequest):
    start = time.monotonic()
    approved, reason = deterministic_check(req)
    latency_ms = int((time.monotonic() - start) * 1000)

    if not approved:
        return {"verdict": "REJECTED", "reason": reason, "latency_ms": latency_ms}

    signed = sign_payload(req.tool_call)
    return {
        "verdict": "APPROVED",
        "signed_jwt": signed,
        "reason": "approved",
        "latency_ms": latency_ms,
    }


@app.get("/healthz")
def health():
    return {"status": "ok"}

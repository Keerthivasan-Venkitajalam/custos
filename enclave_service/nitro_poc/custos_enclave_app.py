"""
Real Custos enclave app (tasks.md T-18). Runs inside the Nitro Enclave.

Boot sequence:
  1. Receive a one-time bootstrap payload over VSOCK (port BOOTSTRAP_PORT): the
     parent's temporary IMDS credentials (needed because the enclave has no
     direct network access and cannot reach the instance metadata service
     itself), the KMS ciphertext of the RSA signing key, and the starting
     mandate ledger.
  2. Decrypt the signing key via `kmstool_enclave_cli decrypt`, which
     generates a real NSM attestation document and calls KMS Decrypt through
     the parent's vsock-proxy. Only succeeds if this enclave's PCR0 matches
     the KMS key policy's condition.
  3. Hold the decrypted private key in memory only — never written to disk.

Runtime: listens on VSOCK (port POLICY_PORT) for tool-call requests, applies
the same deterministic policy as enclave_service/policy_engine.py, and signs
approved payloads locally (no further KMS calls needed after boot).

Known MVP limitation, not hidden: mandate state is in-memory only, seeded at
boot from the bootstrap payload. It resets if the enclave restarts, and can
drift from the Go gateway's Postgres ledger if a signed debit is approved
here but fails downstream for an unrelated reason. A live DB connection
would need a second vsock-proxy tunnel plus a local TCP-to-VSOCK shim (no
Postgres client speaks VSOCK natively) — deliberately out of scope for this
pass; see tasks.md T-18 for the tracked follow-up.
"""
import base64
import json
import socket
import subprocess
import time
import uuid

import jwt as pyjwt

BOOTSTRAP_PORT = 8001
POLICY_PORT = 5005
KMS_PROXY_PORT = 8000
JWT_TTL_SECONDS = 5

KMSTOOL_BIN = "/app/kmstool_enclave_cli"


def vsock_recv_json(port: int) -> dict:
    s = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
    s.bind((socket.VMADDR_CID_ANY, port))
    s.listen()
    print(f"waiting for payload on vsock port {port}", flush=True)
    conn, addr = s.accept()
    chunks = []
    while True:
        chunk = conn.recv(65536)
        if not chunk:
            break
        chunks.append(chunk)
    conn.close()
    s.close()
    return json.loads(b"".join(chunks).decode("utf-8"))


def kms_decrypt(ciphertext_b64: str, region: str, access_key: str, secret_key: str, session_token: str) -> bytes:
    result = subprocess.run(
        [
            KMSTOOL_BIN, "decrypt",
            "--region", region,
            "--proxy-port", str(KMS_PROXY_PORT),
            "--aws-access-key-id", access_key,
            "--aws-secret-access-key", secret_key,
            "--aws-session-token", session_token,
            "--ciphertext", ciphertext_b64,
        ],
        capture_output=True, text=True, timeout=30,
    )
    if result.returncode != 0:
        raise RuntimeError(f"kmstool decrypt failed: {result.stderr}")

    line = next((l for l in result.stdout.splitlines() if l.startswith("PLAINTEXT: ")), None)
    if line is None:
        raise RuntimeError(f"unexpected kmstool output: {result.stdout!r}")

    plaintext_b64 = line[len("PLAINTEXT: "):].strip()
    return base64.b64decode(plaintext_b64)


def deterministic_check(mandates: dict, raw_prompt: str, tool_call: dict) -> tuple[bool, str]:
    if tool_call.get("name") != "execute_upi_reserve_debit":
        return False, "unknown_tool"

    amount = tool_call.get("amount", -1)
    if amount <= 0:
        return False, "invalid_amount"

    mandate_id = tool_call.get("mandate_id")
    mandate = mandates.get(mandate_id)
    if mandate is None:
        return False, "unknown_mandate"

    remaining = mandate["total_blocked"] - mandate["utilized"]
    if amount > remaining:
        return False, "mandate_exceeded"

    recipient = tool_call.get("recipient_vpa", "")
    amount_str = f"{amount:.0f}"
    amount_str_2dp = f"{amount:.2f}"
    mentions_amount = amount_str in raw_prompt or amount_str_2dp in raw_prompt
    mentions_recipient = recipient in raw_prompt
    if not mentions_amount and not mentions_recipient:
        return False, "semantic_mismatch"

    return True, "approved"


def sign_payload(private_key_pem: bytes, tool_call: dict) -> str:
    now = int(time.time())
    claims = {
        "mandate_id": tool_call["mandate_id"],
        "amount": tool_call["amount"],
        "recipient_vpa": tool_call["recipient_vpa"],
        "jti": str(uuid.uuid4()),
        "iat": now,
        "exp": now + JWT_TTL_SECONDS,
    }
    return pyjwt.encode(claims, private_key_pem, algorithm="RS256")


def main():
    bootstrap = vsock_recv_json(BOOTSTRAP_PORT)
    print("bootstrap payload received, decrypting signing key via KMS...", flush=True)

    private_key_pem = kms_decrypt(
        bootstrap["Ciphertext"],
        bootstrap.get("Region", "eu-north-1"),
        bootstrap["AccessKeyId"],
        bootstrap["SecretAccessKey"],
        bootstrap["SessionToken"],
    )
    print("signing key decrypted and held in memory (never written to disk)", flush=True)

    mandates = bootstrap["InitialMandates"]

    s = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
    s.bind((socket.VMADDR_CID_ANY, POLICY_PORT))
    s.listen()
    print(f"policy engine listening on vsock port {POLICY_PORT}", flush=True)

    while True:
        conn, addr = s.accept()
        try:
            data = b""
            while True:
                chunk = conn.recv(65536)
                data += chunk
                if len(chunk) < 65536:
                    break
            req = json.loads(data.decode("utf-8"))

            start = time.monotonic()
            approved, reason = deterministic_check(mandates, req["raw_prompt"], req["tool_call"])
            latency_ms = int((time.monotonic() - start) * 1000)

            if approved:
                mandates[req["tool_call"]["mandate_id"]]["utilized"] += req["tool_call"]["amount"]
                signed = sign_payload(private_key_pem, req["tool_call"])
                resp = {"verdict": "APPROVED", "signed_jwt": signed, "reason": "approved", "latency_ms": latency_ms}
            else:
                resp = {"verdict": "REJECTED", "reason": reason, "latency_ms": latency_ms}

            conn.sendall(json.dumps(resp).encode("utf-8"))
        except Exception as e:
            print(f"error handling request: {e}", flush=True)
            try:
                conn.sendall(json.dumps({"verdict": "REJECTED", "reason": "internal_error"}).encode("utf-8"))
            except Exception:
                pass
        finally:
            conn.close()


if __name__ == "__main__":
    main()

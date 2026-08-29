"""
Runs on the EC2 PARENT instance (not inside the enclave). Exposes the exact
same HTTP contract as the local dev stub (enclave_service/policy_engine.py's
POST /guardrail/evaluate), so the orchestrator's code needs zero changes to
talk to the real enclave instead of the local stub — only ENCLAVE_URL's
tunnel target changes, not the orchestrator itself.
"""
import json
import socket

from fastapi import FastAPI, Request

ENCLAVE_CID = 16
ENCLAVE_PORT = 5005

app = FastAPI(title="custos-vsock-http-bridge")


@app.post("/guardrail/evaluate")
async def evaluate(request: Request):
    payload = await request.json()

    s = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
    s.settimeout(10)
    s.connect((ENCLAVE_CID, ENCLAVE_PORT))
    s.sendall(json.dumps(payload).encode("utf-8"))
    s.shutdown(socket.SHUT_WR)

    chunks = []
    while True:
        chunk = s.recv(65536)
        if not chunk:
            break
        chunks.append(chunk)
    s.close()

    return json.loads(b"".join(chunks).decode("utf-8"))


@app.get("/healthz")
def health():
    return {"status": "ok", "bridging_to": f"vsock cid={ENCLAVE_CID} port={ENCLAVE_PORT}"}

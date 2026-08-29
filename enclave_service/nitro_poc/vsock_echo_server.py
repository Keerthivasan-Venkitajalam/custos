"""
Runs inside the Nitro Enclave. Proves the VSOCK path end to end (tasks.md T-16)
before any real policy-engine logic is baked in — deliberately minimal so the
PCR measurements produced here are the toolchain's baseline, not tangled up
with application logic that will change again in T-18.
"""
import socket

PORT = 5005

s = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
s.bind((socket.VMADDR_CID_ANY, PORT))
s.listen()
print(f"custos vsock echo listening on port {PORT}", flush=True)

while True:
    conn, addr = s.accept()
    print(f"connection from cid={addr}", flush=True)
    data = conn.recv(4096)
    print(f"received: {data!r}", flush=True)
    conn.sendall(data)
    conn.close()

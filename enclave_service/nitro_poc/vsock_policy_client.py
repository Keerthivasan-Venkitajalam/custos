"""Test client for the real enclave policy engine — runs on the parent."""
import json
import socket
import sys

enclave_cid = int(sys.argv[1])
port = 5005
payload = json.loads(sys.argv[2])

s = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
s.connect((enclave_cid, port))
s.sendall(json.dumps(payload).encode("utf-8"))
s.shutdown(socket.SHUT_WR)

chunks = []
while True:
    chunk = s.recv(65536)
    if not chunk:
        break
    chunks.append(chunk)
s.close()

print(json.loads(b"".join(chunks).decode("utf-8")))

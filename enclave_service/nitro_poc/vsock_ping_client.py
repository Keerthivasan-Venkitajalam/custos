"""
Runs on the PARENT instance (not inside the enclave). Connects out to the
enclave's CID and proves a payload round-trips correctly (tasks.md T-16 DoD).
"""
import socket
import sys

enclave_cid = int(sys.argv[1])
port = 5005
payload = b"custos-ping"

s = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
s.connect((enclave_cid, port))
s.sendall(payload)
reply = s.recv(4096)
s.close()

print(f"sent:     {payload!r}")
print(f"received: {reply!r}")
print("PASS" if reply == payload else "FAIL")

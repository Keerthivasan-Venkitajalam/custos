#!/bin/bash
# Runs on the EC2 host (parent), not inside the enclave.
# 1. Starts vsock-proxy so the enclave can reach KMS (enclave has no direct
#    network access at all — this is its only path out).
# 2. Fetches this instance's own IMDS role credentials and relays them into
#    the enclave, since the enclave cannot reach IMDS itself either.
# 3. Sends the sealed private key ciphertext + starting mandate ledger.
set -euo pipefail

REGION="${REGION:-eu-north-1}"
ENCLAVE_CID="${ENCLAVE_CID:-16}"
KMS_PROXY_PORT="${KMS_PROXY_PORT:-8000}"
BOOTSTRAP_PORT="${BOOTSTRAP_PORT:-8001}"
CIPHERTEXT_FILE="${CIPHERTEXT_FILE:-$HOME/custos-enclave/kms-sealed/signing_private_key.pem.encrypted.b64}"

echo "starting vsock-proxy for KMS (${REGION})..."
pkill -f "vsock-proxy ${KMS_PROXY_PORT}" 2>/dev/null || true
nohup vsock-proxy "${KMS_PROXY_PORT}" "kms.${REGION}.amazonaws.com" 443 \
  > "$HOME/vsock-proxy.log" 2>&1 &
sleep 1

echo "fetching instance role credentials from IMDS..."
TOKEN=$(curl -sX PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 21600")
PROFILE=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" http://169.254.169.254/latest/meta-data/iam/security-credentials/)
CREDS=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" "http://169.254.169.254/latest/meta-data/iam/security-credentials/${PROFILE}")

ACCESS_KEY_ID=$(echo "$CREDS" | jq -r '.AccessKeyId')
SECRET_ACCESS_KEY=$(echo "$CREDS" | jq -r '.SecretAccessKey')
SESSION_TOKEN=$(echo "$CREDS" | jq -r '.Token')
CIPHERTEXT=$(cat "$CIPHERTEXT_FILE")

# Starting mandate ledger — demo defaults, override via MANDATES_JSON env var.
MANDATES_JSON="${MANDATES_JSON:-{\"11111111-1111-1111-1111-111111111111\":{\"total_blocked\":10000.00,\"utilized\":0}}}"

PAYLOAD=$(jq -n \
  --arg ak "$ACCESS_KEY_ID" \
  --arg sk "$SECRET_ACCESS_KEY" \
  --arg st "$SESSION_TOKEN" \
  --arg ct "$CIPHERTEXT" \
  --arg region "$REGION" \
  --argjson mandates "$MANDATES_JSON" \
  '{AccessKeyId: $ak, SecretAccessKey: $sk, SessionToken: $st, Ciphertext: $ct, Region: $region, InitialMandates: $mandates}')

echo "sending bootstrap payload to enclave (cid=${ENCLAVE_CID}, port=${BOOTSTRAP_PORT})..."
echo "$PAYLOAD" | socat - VSOCK-CONNECT:"${ENCLAVE_CID}":"${BOOTSTRAP_PORT}"

echo "bootstrap sent. Enclave should now be decrypting its signing key via KMS."

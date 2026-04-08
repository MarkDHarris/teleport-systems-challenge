#!/usr/bin/env bash

# Generates a self-signed CA and test certificates for mTLS

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

DAYS_CA=3650
DAYS_CERT=365
KEY_BITS=4096

OUT_DIR="./certs"
mkdir -p "$OUT_DIR"

echo "-- Generating CA --"
openssl req -x509 -newkey "rsa:${KEY_BITS}" \
    -keyout "${OUT_DIR}/ca.key" -out "${OUT_DIR}/ca.crt" \
    -days "$DAYS_CA" -nodes \
    -subj "/CN=TeleportWorker Test CA/O=Teleport"

echo "-- Generating server certificate --"
# Server cert:
#   - SAN for localhost
#   - EKU = serverAuth
openssl req -newkey "rsa:${KEY_BITS}" \
    -nodes \
    -keyout "${OUT_DIR}/server.key" -out "${OUT_DIR}/server.csr" \
    -subj "/CN=localhost/O=Teleport"

openssl x509 -req -in "${OUT_DIR}/server.csr" \
    -CA "${OUT_DIR}/ca.crt" -CAkey "${OUT_DIR}/ca.key" -CAcreateserial \
    -out "${OUT_DIR}/server.crt" -days "$DAYS_CERT" \
    -extfile <(printf "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\nkeyUsage=digitalSignature,keyEncipherment")

rm -f "${OUT_DIR}/server.csr"


generate_client_cert() {
    local name="$1" cn="$2" ou="$3"

    echo "-- Generating client certificate: ${name} (CN=${cn}, OU=${ou}) --"

    openssl req -newkey "rsa:${KEY_BITS}" -nodes \
        -keyout "${OUT_DIR}/${name}.key" \
        -out "${OUT_DIR}/${name}.csr" \
        -subj "/CN=${cn}/OU=${ou}/O=Teleport"

    openssl x509 -req -in "${OUT_DIR}/${name}.csr" \
        -CA "${OUT_DIR}/ca.crt" -CAkey "${OUT_DIR}/ca.key" -CAcreateserial \
        -out "${OUT_DIR}/${name}.crt" -days "$DAYS_CERT" \
        -extfile <(printf "extendedKeyUsage=clientAuth\nkeyUsage=digitalSignature")

    rm -f "${OUT_DIR}/${name}.csr"
}

generate_client_cert "mark" "mark" "eng"
generate_client_cert "tim"   "tim"   "eng"
generate_client_cert "admin" "admin" "admin"

rm -f "${OUT_DIR}/ca.srl"

echo ""
echo "-- Certificates generated --"
echo "  Output dir: ${OUT_DIR}"
echo "  CA:     ca.crt, ca.key"
echo "  Server: server.crt, server.key"
echo "  Mark:   mark.crt, mark.key    (CN=mark, OU=eng)"
echo "  Tim:    tim.crt, tim.key      (CN=tim, OU=eng)"
echo "  Admin:  admin.crt, admin.key   (CN=admin, OU=admin)"
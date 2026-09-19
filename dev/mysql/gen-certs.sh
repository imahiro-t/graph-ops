#!/bin/sh
# Generates a throwaway CA and a server certificate for the dev MySQL in
# compose.yaml. The server certificate names localhost and 127.0.0.1, so
# GraphOps' "verify-full" mode works against it when given certs/ca.pem.
# Existing certificates are kept; pass --force to regenerate them.
set -eu

cd "$(dirname "$0")"
mkdir -p certs

if [ -f certs/ca.pem ] && [ "${1:-}" != "--force" ]; then
  echo "certs/ already exists (pass --force to regenerate)"
  exit 0
fi

openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -subj "/CN=GraphOps Dev MySQL CA" \
  -keyout certs/ca-key.pem -out certs/ca.pem

openssl req -newkey rsa:2048 -nodes \
  -subj "/CN=localhost" \
  -keyout certs/server-key.pem -out certs/server.csr

printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > certs/server-ext.cnf
openssl x509 -req -days 3650 -in certs/server.csr \
  -CA certs/ca.pem -CAkey certs/ca-key.pem -CAcreateserial \
  -extfile certs/server-ext.cnf -out certs/server-cert.pem

rm -f certs/server.csr certs/server-ext.cnf certs/ca.srl
# mysqld runs as a different user inside the container and must be able to
# read the key through the read-only bind mount. Dev-only material.
chmod 644 certs/*.pem
echo "wrote $(pwd)/certs"

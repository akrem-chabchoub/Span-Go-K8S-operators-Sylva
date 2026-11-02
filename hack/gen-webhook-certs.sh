#!/bin/sh
set -eu

out=${1:-bin/certs}
mkdir -p "$out"

cat > "$out/san.cnf" <<'EOF'
[req]
distinguished_name = dn
x509_extensions = v3
prompt = no
[dn]
CN = span-webhook.span-system.svc
[v3]
subjectAltName = DNS:span-webhook.span-system.svc,DNS:span-webhook.span-system.svc.cluster.local
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
basicConstraints = CA:true
EOF

openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \
  -keyout "$out/tls.key" -out "$out/tls.crt" \
  -config "$out/san.cnf"

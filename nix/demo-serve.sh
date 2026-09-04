# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
server_binary="$BOND_EXCHANGE_RUNTIME_ROOT/bond-exchange-server"
rest_address="${BOND_EXCHANGE_ADDRESS:-:8080}"
grpc_address="${BOND_EXCHANGE_GRPC_ADDRESS:-:9090}"
rest_display="$rest_address"
grpc_display="$grpc_address"
if [[ "$rest_display" == :* ]]; then
  rest_display="localhost$rest_display"
fi
if [[ "$grpc_display" == :* ]]; then
  grpc_display="localhost$grpc_display"
fi

psql -v ON_ERROR_STOP=1 -f "$project_root/db/demo/seed.sql"
go build -o "$server_binary" ./cmd/server
auth_root="$BOND_EXCHANGE_RUNTIME_ROOT/auth"
go run ./cmd/demo-auth init "$auth_root"
export BOND_EXCHANGE_ASSERTION_ISSUER="https://demo-issuer.invalid"
export BOND_EXCHANGE_ASSERTION_AUDIENCE="bond-exchange-demo"
export BOND_EXCHANGE_ASSERTION_JWKS_FILE="$auth_root/public.jwks"

echo "Bond Exchange demo is ready when GET http://$rest_display/healthz returns healthy."
echo "REST: http://$rest_display  gRPC: $grpc_display"
echo "Seeded bonds: DEMO2026 and DEMO2027; buyer: demo-buyer; seller: demo-seller"
echo "Ephemeral demo assertion key: $auth_root/private.jwk"

exec "$server_binary"

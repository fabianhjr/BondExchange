# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
artifact_root="$project_root/.artifacts"
mkdir -p "$artifact_root"

otelcol-contrib validate --config "$project_root/nix/otel-collector.yaml"

cd "$project_root/application"
set +e
go test -count=1 -json \
  ./cmd/server \
  ./internal/authn \
  ./internal/eventing \
  ./internal/exchangerates \
  ./internal/httpapi \
  ./internal/offerintake \
  ./internal/rpcapi \
  ./internal/sie \
  ./internal/telemetry \
  >"$artifact_root/observability-test.json"
test_status=$?
set -e
if (( test_status != 0 )); then
  cat "$artifact_root/observability-test.json" >&2
  exit "$test_status"
fi

echo "OpenTelemetry configuration and contract tests passed"

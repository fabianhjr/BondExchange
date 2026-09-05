# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
cd "$project_root"

# The security-focused Go tests include the PostgreSQL integration package.
# Without the disposable harness those tests skip silently, so this check would
# report success while verifying nothing about persistence.
if [[ -z "${BOND_EXCHANGE_TEST_DATABASE_URL:-}" ]]; then
  echo "security:check requires the disposable PostgreSQL harness." >&2
  echo "Run it as a devenv task rather than invoking this script directly." >&2
  exit 1
fi

mkdir -p .artifacts
bond-exchange-asvs-source-check
bond-exchange-asvs-profile-check
(cd application && go list -m -json all) >.artifacts/go-modules.json
(cd application && govulncheck ./...)
(cd application && go test ./internal/authn ./internal/httpapi ./internal/postgres ./internal/rpcapi)

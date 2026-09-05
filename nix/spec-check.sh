# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
specification_directory="$project_root/spec/tla"
metadata_directory="$project_root/.devenv/tlc"

cd "$specification_directory"

# Marketplace, authorization, and liveness behavior are checked as separate
# finite instances. Their state spaces multiply, and each concern needs
# different constants to stay falsifiable, so one combined instance would
# either be intractable or too small to reach the interesting interleavings.
configurations=(
  "BondExchange.cfg"
  "BondExchangeAuthorization.cfg"
  "BondExchangeLiveness.cfg"
)

for configuration in "${configurations[@]}"; do
  if [[ ! -f "$configuration" ]]; then
    echo "missing TLC configuration $configuration" >&2
    exit 1
  fi
done

failures=0

for configuration in "${configurations[@]}"; do
  echo "==> TLC $configuration"
  output_file="$(mktemp)"
  status=0
  tlc \
    -workers 1 \
    -cleanup \
    -coverage 1 \
    -metadir "$metadata_directory/${configuration%.cfg}" \
    -config "$configuration" \
    BondExchange.tla >"$output_file" 2>&1 || status=$?

  if [[ $status -ne 0 ]] || ! grep -q "No error has been found" "$output_file"; then
    cat "$output_file" >&2
    echo "TLC rejected $configuration" >&2
    rm -f "$output_file"
    exit 1
  fi

  grep -E "^[0-9,]+ states generated" "$output_file" || true

  # An action that is never enabled makes every property that depends on it
  # vacuously true. TLC reports each action as "distinct:total"; a self-looping
  # action such as an idempotent retry contributes no distinct state, so the
  # total is the number that must be non-zero.
  uncovered="$(
    grep -E '^<[^>]+>: [0-9]+:[0-9]+$' "$output_file" |
      sed -E 's/^<([A-Za-z]+)[^>]*>: [0-9]+:([0-9]+)$/\1 \2/' |
      awk '$2 == 0 { print $1 }' |
      sort -u
  )"

  if [[ -n "$uncovered" ]]; then
    echo "$configuration left these actions unreachable:" >&2
    while IFS= read -r action; do
      printf '  %s\n' "$action" >&2
    done <<<"$uncovered"
    failures=$((failures + 1))
  fi

  rm -f "$output_file"
done

if [[ $failures -ne 0 ]]; then
  echo "every action must be reachable in the instance that checks it" >&2
  exit 1
fi

echo "TLC checked every configured instance and reached every action"

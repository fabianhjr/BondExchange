# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
cd "$project_root"

configuration="devenv.nix"
workflow=".github/workflows/go-quality.yml"

# `devenv test` runs every task that names "devenv:enterTest" in its `before`
# list, while continuous integration runs the aggregate tasks below as separate
# jobs. A gate attached directly to `devenv:enterTest` would therefore run
# locally and never run in CI, so the two entry points must stay equivalent by
# construction rather than by review.
aggregates=(
  "dev:ci"
  "go:mutation"
)

for file in "$configuration" "$workflow"; do
  if [[ ! -f "$file" ]]; then
    echo "missing $file" >&2
    exit 1
  fi
done

indent() {
  while IFS= read -r line; do
    printf '  %s\n' "$line"
  done
}

attached="$(
  awk '
    match($0, /^  tasks\."[^"]+"/) {
      task = $0
      sub(/^  tasks\."/, "", task)
      sub(/".*$/, "", task)
    }
    /"devenv:enterTest"/ { print task }
  ' "$configuration" | sort -u
)"
expected="$(printf '%s\n' "${aggregates[@]}" | sort -u)"

if [[ "$attached" != "$expected" ]]; then
  {
    echo "Only the aggregate tasks may attach to devenv:enterTest."
    echo "Add a new gate to an aggregate's \`after\` list instead, so that"
    echo "$workflow keeps running everything that devenv test runs."
    echo "expected:"
    printf '%s\n' "$expected" | indent
    echo "found:"
    printf '%s\n' "$attached" | indent
  } >&2
  exit 1
fi

for aggregate in "${aggregates[@]}"; do
  if ! grep -qF "  tasks.\"$aggregate\" = {" "$configuration"; then
    echo "$configuration does not declare the aggregate task $aggregate" >&2
    exit 1
  fi
  if ! grep -qF "devenv tasks run $aggregate" "$workflow"; then
    echo "$workflow does not run the aggregate task $aggregate" >&2
    exit 1
  fi
done

echo "devenv test and $workflow run the same gates: ${aggregates[*]}"

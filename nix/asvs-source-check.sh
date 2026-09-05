# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
cd "$project_root"

submodule="third_party/asvs"
expected_commit="5cf9b032440be53ce345ab3c130fda46ba1ce7a2"
expected_tag="v5.0.0_release"
source_directory="$submodule/5.0/en"
profile="docs/security/asvs-5.0.0-l3.tsv"
narrative="docs/security/ASVS.md"

if [[ ! -d "$source_directory" ]]; then
  echo "The pinned ASVS source is not checked out at $source_directory." >&2
  echo "Run: git submodule update --init --depth 1 $submodule" >&2
  exit 1
fi

# `git submodule status` prefixes the commit with "-" when the submodule is not
# initialized and "+" when the checked-out commit differs from the one this
# repository records, so an accidental upstream bump cannot pass unnoticed.
status_line="$(git submodule status "$submodule")"
case "$status_line" in
  -*)
    echo "The ASVS submodule is not initialized: $status_line" >&2
    echo "Run: git submodule update --init --depth 1 $submodule" >&2
    exit 1
    ;;
  +*)
    echo "The ASVS submodule is not at the commit this repository records:" >&2
    echo "  $status_line" >&2
    echo "Run: git submodule update --init --depth 1 $submodule" >&2
    exit 1
    ;;
esac

actual_commit="$(git -C "$submodule" rev-parse HEAD)"
if [[ "$actual_commit" != "$expected_commit" ]]; then
  echo "ASVS source commit is $actual_commit, want $expected_commit ($expected_tag)." >&2
  echo "Changing the assessment baseline requires reviewing every disposition." >&2
  exit 1
fi

# The assessed baseline is documented as well as pinned; both must agree.
for reference in "$expected_commit" "$expected_tag"; do
  if ! grep -qF "$reference" "$narrative"; then
    echo "$narrative does not record the pinned ASVS baseline $reference" >&2
    exit 1
  fi
done

# ASVS publishes each requirement as a Markdown table row of the form
# "| **1.2.3** | Verify that ... | 2 |". Chapter files are 0x10 through 0x26;
# the frontispiece and appendices contain no requirement rows.
extract_source() {
  awk -F'|' '
    $2 ~ /^ *\*\*[0-9]+\.[0-9]+\.[0-9]+\*\* *$/ {
      id = $2
      gsub(/[ *]/, "", id)
      level = $4
      gsub(/[ *]/, "", level)
      print id "\t" level
    }
  ' "$source_directory"/0x1*.md "$source_directory"/0x2*.md | sort -V
}

extract_profile() {
  awk -F'\t' '!/^#/ && NF { print $1 "\t" $2 }' "$profile" | sort -V
}

source_requirements="$(extract_source)"
profile_requirements="$(extract_profile)"

if [[ -z "$source_requirements" ]]; then
  echo "No requirements were extracted from $source_directory; the upstream format changed." >&2
  exit 1
fi

if [[ "$source_requirements" != "$profile_requirements" ]]; then
  echo "$profile does not match the pinned ASVS source." >&2
  echo "Requirement identifiers and levels present in only one of them:" >&2
  diff <(printf '%s\n' "$source_requirements") <(printf '%s\n' "$profile_requirements") >&2 || true
  exit 1
fi

count="$(printf '%s\n' "$source_requirements" | wc -l)"
echo "ASVS profile matches all $count requirement identifiers and levels in $expected_tag ($expected_commit)"

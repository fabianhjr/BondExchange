# shellcheck shell=bash
set -euo pipefail
export LC_ALL=C

project_root="${DEVENV_ROOT:-$PWD}"
cd "$project_root"

vendor="third_party/asvs"
expected_commit="5cf9b032440be53ce345ab3c130fda46ba1ce7a2"
expected_tag="v5.0.0_release"
source_directory="$vendor/5.0/en"
manifest="$vendor/SHA256SUMS"
provenance="$vendor/SOURCE.md"
profile="docs/security/asvs-5.0.0-l3.tsv"
narrative="docs/security/ASVS.md"

chapters=(
  "5.0/en/0x10-V1-Encoding-and-Sanitization.md"
  "5.0/en/0x11-V2-Validation-and-Business-Logic.md"
  "5.0/en/0x12-V3-Web-Frontend-Security.md"
  "5.0/en/0x13-V4-API-and-Web-Service.md"
  "5.0/en/0x14-V5-File-Handling.md"
  "5.0/en/0x15-V6-Authentication.md"
  "5.0/en/0x16-V7-Session-Management.md"
  "5.0/en/0x17-V8-Authorization.md"
  "5.0/en/0x18-V9-Self-contained-Tokens.md"
  "5.0/en/0x19-V10-OAuth-and-OIDC.md"
  "5.0/en/0x20-V11-Cryptography.md"
  "5.0/en/0x21-V12-Secure-Communication.md"
  "5.0/en/0x22-V13-Configuration.md"
  "5.0/en/0x23-V14-Data-Protection.md"
  "5.0/en/0x24-V15-Secure-Coding-and-Architecture.md"
  "5.0/en/0x25-V16-Security-Logging-and-Error-Handling.md"
  "5.0/en/0x26-V17-WebRTC.md"
)
manifest_files=("LICENSE.md" "${chapters[@]}")

for required in "$manifest" "$provenance" "$profile" "$narrative"; do
  if [[ ! -f "$required" ]]; then
    echo "Missing ASVS verification input: $required" >&2
    exit 1
  fi
done

for relative_path in "${manifest_files[@]}"; do
  if [[ ! -f "$vendor/$relative_path" ]]; then
    echo "Missing vendored ASVS file: $vendor/$relative_path" >&2
    exit 1
  fi
done

# The inventory is deliberate: adding another chapter must not silently expand
# the assessment input beyond the files attributed and pinned in SOURCE.md.
shopt -s nullglob
actual_chapters=("$source_directory"/*.md)
if [[ "${#actual_chapters[@]}" -ne "${#chapters[@]}" ]]; then
  echo "$source_directory contains ${#actual_chapters[@]} Markdown chapters, want ${#chapters[@]}" >&2
  exit 1
fi
for index in "${!chapters[@]}"; do
  if [[ "${actual_chapters[$index]}" != "$vendor/${chapters[$index]}" ]]; then
    echo "Unexpected ASVS chapter inventory in $source_directory" >&2
    exit 1
  fi
done

expected_manifest_files="$(printf '%s\n' "${manifest_files[@]}")"
actual_manifest_files="$(awk '
  NF != 2 || length($1) != 64 || $1 !~ /^[0-9a-f]+$/ {
    print "invalid checksum manifest row at line " NR > "/dev/stderr"
    failed = 1
    next
  }
  { print $2 }
  END { exit failed }
' "$manifest")" || exit 1
if [[ "$actual_manifest_files" != "$expected_manifest_files" ]]; then
  echo "$manifest does not enumerate exactly the licensed ASVS snapshot." >&2
  diff <(printf '%s\n' "$expected_manifest_files") <(printf '%s\n' "$actual_manifest_files") >&2 || true
  exit 1
fi

if ! (cd "$vendor" && sha256sum --check --quiet --strict SHA256SUMS); then
  echo "The vendored ASVS snapshot does not match its recorded checksums." >&2
  echo "Changing the assessment baseline requires reviewing every disposition." >&2
  exit 1
fi

# The assessed baseline is documented as well as pinned; all records must agree.
for document in "$provenance" "$narrative"; do
  for reference in "$expected_commit" "$expected_tag"; do
    if ! grep -qF "$reference" "$document"; then
      echo "$document does not record the pinned ASVS baseline $reference" >&2
      exit 1
    fi
  done
done

# ASVS publishes each requirement as a Markdown table row of the form
# "| **1.2.3** | Verify that ... | 2 |". Only the 17 explicitly pinned
# requirement chapters participate in the comparison.
extract_source() {
  local source_files=()
  local chapter
  for chapter in "${chapters[@]}"; do
    source_files+=("$vendor/$chapter")
  done
  awk -F'|' '
    $2 ~ /^ *\*\*[0-9]+\.[0-9]+\.[0-9]+\*\* *$/ {
      id = $2
      gsub(/[ *]/, "", id)
      level = $4
      gsub(/[ *]/, "", level)
      print id "\t" level
    }
  ' "${source_files[@]}" | sort -V
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
echo "ASVS profile matches all $count requirement identifiers and levels in the vendored $expected_tag snapshot ($expected_commit)"

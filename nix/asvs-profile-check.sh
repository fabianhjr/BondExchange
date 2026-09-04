# shellcheck shell=bash
set -euo pipefail

profile="docs/security/asvs-5.0.0-l3.tsv"
expected_count=345

if [[ ! -f "$profile" ]]; then
  echo "missing ASVS profile: $profile" >&2
  exit 1
fi

actual_count="$(awk -F '\t' '!/^#/ && NF { count++ } END { print count + 0 }' "$profile")"
if [[ "$actual_count" != "$expected_count" ]]; then
  echo "ASVS profile contains $actual_count requirements, want $expected_count" >&2
  exit 1
fi

if ! awk -F '\t' '
  /^#/ || !NF { next }
  NF != 5 { print "invalid field count at line " NR > "/dev/stderr"; failed = 1; next }
  $1 !~ /^[0-9]+\.[0-9]+\.[0-9]+$/ { print "invalid requirement ID " $1 > "/dev/stderr"; failed = 1 }
  $2 !~ /^[123]$/ { print "invalid level for " $1 > "/dev/stderr"; failed = 1 }
  $3 !~ /^(verified|not-applicable|pending-deployment|pending-external-identity)$/ {
    print "invalid disposition for " $1 > "/dev/stderr"; failed = 1
  }
  seen[$1]++ > 0 { print "duplicate requirement " $1 > "/dev/stderr"; failed = 1 }
  $3 == "verified" && ($4 == "" || $4 == "-") {
    print "verified requirement lacks evidence: " $1 > "/dev/stderr"; failed = 1
  }
  END { exit failed }
' "$profile"; then
  exit 1
fi

while IFS=$'\t' read -r requirement _ status evidence _; do
  [[ "$requirement" == \#* || -z "$requirement" || "$status" != "verified" ]] && continue
  if [[ ! -e "$evidence" ]]; then
    echo "evidence for $requirement does not exist: $evidence" >&2
    exit 1
  fi
done < "$profile"

echo "ASVS 5.0.0 Level 3 application profile covers all $expected_count requirements"

# shellcheck shell=bash
set -euo pipefail

project_root="${DEVENV_ROOT:-$PWD}"
cd "$project_root"

failures=0

fail() {
  echo "$1" >&2
  failures=$((failures + 1))
}

mapfile -t documents < <(
  find . -name '*.md' -not -path './third_party/*' -not -path './.git/*' -not -path './.devenv/*' |
    sed 's|^\./||' |
    sort
)

if ((${#documents[@]} == 0)); then
  echo "no Markdown documents found" >&2
  exit 1
fi

# GitHub derives a heading anchor by lowercasing the text, discarding every
# character that is not alphanumeric, a space, or a hyphen, and replacing the
# remaining spaces with hyphens. Fenced code blocks contain no headings.
heading_anchors() {
  awk '
    /^```/ { fenced = !fenced; next }
    !fenced && /^#{1,6} / {
      anchor = $0
      sub(/^#+ +/, "", anchor)
      anchor = tolower(anchor)
      gsub(/[^a-z0-9 -]/, "", anchor)
      gsub(/ /, "-", anchor)
      print anchor
    }
  ' "$1"
}

# Emit every Markdown link target that lies outside a fenced code block.
link_targets() {
  awk '
    /^```/ { fenced = !fenced; next }
    fenced { next }
    {
      line = $0
      while (match(line, /\]\([^)]*\)/)) {
        target = substr(line, RSTART + 2, RLENGTH - 3)
        print target
        line = substr(line, RSTART + RLENGTH)
      }
    }
  ' "$1"
}

for document in "${documents[@]}"; do
  directory="$(dirname "$document")"
  while IFS= read -r target; do
    [[ -z "$target" ]] && continue
    case "$target" in
      http://* | https://* | mailto:*) continue ;;
    esac

    path="${target%%#*}"
    anchor=""
    if [[ "$target" == *"#"* ]]; then
      anchor="${target#*#}"
    fi

    if [[ -z "$path" ]]; then
      resolved="$document"
    elif [[ "$directory" == "." ]]; then
      resolved="$path"
    else
      resolved="$directory/$path"
    fi

    if [[ ! -e "$resolved" ]]; then
      fail "$document: link target does not exist: $target"
      continue
    fi

    if [[ -n "$anchor" && "$resolved" == *.md ]]; then
      if ! heading_anchors "$resolved" | grep -qxF "$anchor"; then
        fail "$document: no heading in $resolved matches anchor #$anchor"
      fi
    fi
  done < <(link_targets "$document")
done

# Every migration is part of the documented schema history.
for migration in db/migrations/*.sql; do
  name="$(basename "$migration")"
  if ! grep -qF "$name" db/README.md; then
    fail "db/README.md does not reference the migration $name"
  fi
done

# Every architecture decision record is listed in its index, and each file's
# title matches the number in its name.
for record in docs/adr/0*.md; do
  name="$(basename "$record")"
  number="${name%%-*}"
  if ! grep -qF "($name)" docs/adr/README.md; then
    fail "docs/adr/README.md does not list $name"
  fi
  if ! grep -qE "^# ADR-$number: " "$record"; then
    fail "$record does not start with a matching \"# ADR-$number:\" title"
  fi
done

# Friction and failure-mode identifiers are referenced across documents by hand.
# A reference to a retired or misspelled identifier must fail rather than rot.
#
# Architecture decision records are exempt. They are kept even when superseded
# so their reasoning stays available, and one legitimately records which
# friction it resolved and removed. Enforcing live identifiers there would
# require editing history whenever a register entry is retired. ADRs reference
# identifiers by name and never link into the registers by anchor, so the link
# checks above still cover their navigable claims.
defined_frictions="$(grep -oE '^### F-[0-9]{3}' FRICTIONS.md | grep -oE 'F-[0-9]{3}' | sort -u)"
defined_failure_modes="$(grep -oE '^### FM-[0-9]{3}' docs/FMEA.md | grep -oE 'FM-[0-9]{3}' | sort -u)"

if [[ -z "$defined_frictions" || -z "$defined_failure_modes" ]]; then
  fail "could not read friction or failure-mode identifiers from their registers"
fi

# Two entries sharing an identifier is how a concurrent branch silently reuses a
# number. The registers forbid reuse precisely because references are by
# identifier, so a duplicate definition must fail rather than resolve to
# whichever heading a reader happens to reach first.
duplicate_identifiers() {
  grep -oE "^### $1-[0-9]{3}" "$2" | grep -oE "$1-[0-9]{3}" | sort | uniq -d
}

while IFS= read -r identifier; do
  [[ -z "$identifier" ]] && continue
  fail "FRICTIONS.md defines $identifier more than once"
done < <(duplicate_identifiers F FRICTIONS.md)

while IFS= read -r identifier; do
  [[ -z "$identifier" ]] && continue
  fail "docs/FMEA.md defines $identifier more than once"
done < <(duplicate_identifiers FM docs/FMEA.md)

for document in "${documents[@]}"; do
  case "$document" in
    docs/adr/*) continue ;;
  esac

  while IFS= read -r identifier; do
    [[ -z "$identifier" ]] && continue
    if ! printf '%s\n' "$defined_frictions" | grep -qxF "$identifier"; then
      fail "$document references $identifier, which FRICTIONS.md does not define"
    fi
  done < <(grep -oE '\bF-[0-9]{3}\b' "$document" | sort -u)

  while IFS= read -r identifier; do
    [[ -z "$identifier" ]] && continue
    if ! printf '%s\n' "$defined_failure_modes" | grep -qxF "$identifier"; then
      fail "$document references $identifier, which docs/FMEA.md does not define"
    fi
  done < <(grep -oE '\bFM-[0-9]{3}\b' "$document" | sort -u)
done

if ((failures > 0)); then
  echo "$failures documentation integrity problem(s) found" >&2
  exit 1
fi

echo "Documentation integrity verified across ${#documents[@]} Markdown documents"

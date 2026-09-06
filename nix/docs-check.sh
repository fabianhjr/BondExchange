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

# Friction, failure-mode, and guarantee identifiers are referenced across
# documents by hand. A reference to a retired or misspelled identifier must fail
# rather than rot.
#
# Architecture decision records are exempt. They are kept even when superseded
# so their reasoning stays available, and one legitimately records which
# friction it resolved and removed. Enforcing live identifiers there would
# require editing history whenever a register entry is retired. ADRs reference
# identifiers by name and never link into the registers by anchor, so the link
# checks above still cover their navigable claims.
guarantee_register="docs/guarantees.md"

defined_frictions="$(grep -oE '^### F-[0-9]{3}' FRICTIONS.md | grep -oE 'F-[0-9]{3}' | sort -u)"
defined_failure_modes="$(grep -oE '^### FM-[0-9]{3}' docs/FMEA.md | grep -oE 'FM-[0-9]{3}' | sort -u)"
defined_guarantees="$(grep -oE '^### G-[0-9]{3}' "$guarantee_register" | grep -oE 'G-[0-9]{3}' | sort -u)"

if [[ -z "$defined_frictions" || -z "$defined_failure_modes" || -z "$defined_guarantees" ]]; then
  fail "could not read friction, failure-mode, or guarantee identifiers from their registers"
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

while IFS= read -r identifier; do
  [[ -z "$identifier" ]] && continue
  fail "$guarantee_register defines $identifier more than once"
done < <(duplicate_identifiers G "$guarantee_register")

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

  while IFS= read -r identifier; do
    [[ -z "$identifier" ]] && continue
    if ! printf '%s\n' "$defined_guarantees" | grep -qxF "$identifier"; then
      fail "$document references $identifier, which $guarantee_register does not define"
    fi
  done < <(grep -oE '\bG-[0-9]{3}\b' "$document" | sort -u)
done

# The guarantee register claims that named code, schema, specification, and
# verification objects back each promise. A citation that no longer resolves
# would leave the register asserting a guarantee nothing enforces, so every
# name it prints is checked against the artifact its kind names.

# Only the forward section of each migration describes the deployed schema. A
# down migration may drop an object the current schema still has, and this
# repository rolls corrections forward rather than down.
forward_migration_sql="$(
  for migration in db/migrations/*.sql; do
    awk '/^-- migrate:down/ { exit } { print }' "$migration"
  done
)"

# An object is current when a forward migration defines it and no later forward
# migration drops it. Recreating a view after dropping it therefore passes,
# while contracting an object away fails any guarantee still citing it.
postgres_object_is_current() {
  local name="$1" definition drop
  definition="$(
    printf '%s\n' "$forward_migration_sql" |
      grep -nE "(CREATE|ADD CONSTRAINT|ADD PRIMARY KEY|^[[:space:]]*CONSTRAINT)[^;]*\\b$name\\b" |
      tail -1 | cut -d: -f1
  )" || true
  [[ -z "$definition" ]] && return 1
  drop="$(
    printf '%s\n' "$forward_migration_sql" |
      grep -nE "DROP[^;]*\\b$name\\b" | tail -1 | cut -d: -f1
  )" || true
  [[ -z "$drop" ]] && return 0
  ((definition > drop))
}

# Generated bindings and tests are excluded: a guarantee must be backed by
# hand-written application code, not by a name that only a test mentions.
go_identifier_exists() {
  grep -rqE "\\b$1\\b" --include='*.go' --exclude='*_test.go' \
    application/cmd application/internal
}

proto_declaration_exists() {
  grep -rqE "\\b$1\\b" api/proto
}

# A property that no TLC configuration checks is not evidence, so definition
# alone is not enough.
tla_property_is_checked() {
  grep -qE "^$1 ==" spec/tla/BondExchangeProperties.tla &&
    grep -qE "^[[:space:]]*$1[[:space:]]*$" spec/tla/*.cfg
}

verification_task_exists() {
  grep -qF "tasks.\"$1\"" devenv.nix
}

while IFS= read -r identifier; do
  [[ -z "$identifier" ]] && continue

  entry="$(
    awk -v heading="^### $identifier " '
      $0 ~ heading { inside = 1; next }
      inside && /^#{2,3} / { exit }
      inside { print }
    ' "$guarantee_register"
  )"

  # Every entry states what it promises, the adverse condition the promise
  # survives, what a caller observes, and where the promise stops. An entry
  # missing its boundary reads as an unlimited guarantee.
  for section in '**Promise.**' '**Even when.**' '**You will see.**' '**Not promised.**' '**Enforced by.**'; do
    if ! printf '%s\n' "$entry" | grep -qF "$section"; then
      fail "$guarantee_register: $identifier has no $section section"
    fi
  done

  verified=0
  while IFS= read -r citation; do
    [[ -z "$citation" ]] && continue
    kind="${citation#- }"
    kind="${kind%%:*}"
    # shellcheck disable=SC2016 # the backticks delimit Markdown code spans
    names="$(printf '%s\n' "${citation#*: }" | grep -oE '`[^`]+`' | tr -d '`')" || true
    if [[ -z "$names" ]]; then
      fail "$guarantee_register: $identifier has a $kind line that names nothing"
      continue
    fi
    while IFS= read -r name; do
      [[ -z "$name" ]] && continue
      case "$kind" in
        PostgreSQL)
          postgres_object_is_current "$name" ||
            fail "$guarantee_register: $identifier cites the PostgreSQL object $name, which no forward migration currently defines"
          ;;
        Go)
          go_identifier_exists "$name" ||
            fail "$guarantee_register: $identifier cites the Go identifier $name, which application source does not define"
          ;;
        Proto3)
          proto_declaration_exists "$name" ||
            fail "$guarantee_register: $identifier cites the Proto3 declaration $name, which api/proto does not contain"
          ;;
        'TLA+')
          tla_property_is_checked "$name" ||
            fail "$guarantee_register: $identifier cites the TLA+ property $name, which is not defined and checked by a TLC configuration"
          ;;
        'Verified by')
          verified=1
          verification_task_exists "$name" ||
            fail "$guarantee_register: $identifier cites the verification task $name, which devenv.nix does not define"
          ;;
      esac
    done <<<"$names"
  done < <(printf '%s\n' "$entry" | grep -E '^- (PostgreSQL|Go|Proto3|TLA\+|Verified by): ' || true)

  if ((verified == 0)); then
    fail "$guarantee_register: $identifier names no verifying task"
  fi
done <<<"$defined_guarantees"

if ((failures > 0)); then
  echo "$failures documentation integrity problem(s) found" >&2
  exit 1
fi

echo "Documentation integrity verified across ${#documents[@]} Markdown documents"

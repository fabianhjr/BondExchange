# ADR-0022: Verify documentation against pinned sources

- Status: Accepted
- Date: 2026-09-04

## Context

`AGENTS.md` requires implementation and documentation to change together, and
the repository takes that seriously: `FRICTIONS.md`, `docs/FMEA.md`, the ASVS
profile, the ADRs, and the scoped READMEs cross-reference each other by
hand-written relative links and heading anchors, and by identifiers such as
`F-016` and `FM-014`. None of it was checked. Retitling a heading silently
breaks every anchor pointing at it, and removing a resolved friction leaves
dangling references in the analysis that cited it. The register's own rule that
a retired identifier is never reused exists precisely because these references
are load-bearing, but nothing enforced it.

Two hand-maintained inventories had the same problem. `db/README.md` narrates
every migration by filename, and `docs/adr/README.md` lists every decision
record. Adding a file to either directory without editing the index produced no
failure.

The ASVS profile was worse than unchecked: it was unverifiable. It recorded its
assessment baseline as an absolute path under one contributor's `Downloads`
directory plus a source checksum, and the repository neither contained nor
fetched that source. `asvs-profile-check.sh` validated row shape, count,
dispositions, and evidence-path existence, but nothing tied the 345 rows to the
standard they claimed to cover. The recorded checksum could not be reproduced
by any documented procedure. The path also published a contributor's local
username.

## Decision

Pin the assessment baseline as a submodule. `third_party/asvs` references
[OWASP/ASVS](https://github.com/OWASP/ASVS) at tag `v5.0.0_release`, commit
`5cf9b032440be53ce345ab3c130fda46ba1ce7a2`. Git's commit identifier is the
content address, so no separate checksum is invented. The submodule is marked
shallow in `.gitmodules`. OWASP licenses the standard under Creative Commons
Attribution-ShareAlike 4.0 International; referencing it as a submodule keeps
the upstream license with the upstream text rather than copying requirement
text into this repository.

Verify the profile against that pin. `nix/asvs-source-check.sh`, run as part of
`security:check`, extracts every requirement identifier and level from the
pinned Markdown chapters and requires exact equality with the checked-in TSV. It
also fails when the submodule is missing, when its checked-out commit differs
from the recorded one, and when `docs/security/ASVS.md` does not record the same
tag and commit, so the narrative, the pin, and the profile cannot drift apart.

Verify the documentation graph. `nix/docs-check.sh`, run by the new `docs:check`
task and included in the `dev:ci` aggregate, resolves every relative Markdown
link and heading anchor outside fenced code blocks, requires each migration to
appear in `db/README.md`, requires each ADR to appear in its index with a title
matching its filename number, and requires every `F-NNN` and `FM-NNN` reference
to be defined in `FRICTIONS.md` or `docs/FMEA.md` respectively.

Decision records are exempt from that last rule. An ADR is kept even when
superseded, and one legitimately records which friction it resolved and
removed; enforcing live identifiers there would mean editing history whenever a
register entry retires. ADRs reference identifiers by name and never link into
the registers by anchor, so the link checks still cover their navigable claims.

## Consequences

- The ASVS profile is reproducible by anyone with the repository. F-014 is
  resolved and removed from the register.
- A reference to a retired friction identifier now fails a gate. This directly
  enforces the register's non-reuse rule, which previously depended on review.
- Resolving a friction becomes slightly more work: every citation must be
  updated in the same change. That is the intended cost, and it is exactly the
  work that `AGENTS.md` already required.
- Verifying the baseline requires roughly 160 MB of upstream checkout for the
  332 KB actually read, because the ASVS repository retains every prior version
  of the standard. `security:check` and therefore `devenv test` fail without it.
  F-020 records this cost and its completion conditions.
- The anchor checker implements GitHub's slug algorithm rather than reusing it.
  A future upstream change to that algorithm would make the checker wrong in
  either direction; the rule is simple enough that this is an acceptable risk
  for internal links.
- Re-pinning to a later ASVS release is deliberately disruptive: the source
  check fails until the commit, the narrative, and every affected disposition
  are updated together.

## Alternatives considered

### Vendor the requirement text directly

Copying `5.0/en` into the repository would avoid the 160 MB checkout and the
submodule workflow entirely. It would also copy CC BY-SA licensed text into a
repository that has no license of its own (F-021), and it would replace a
verifiable upstream pin with a local copy whose provenance depends on the
diligence of whoever pasted it. A submodule keeps provenance mechanical.

### Keep a checksum of a downloaded release archive

Fetching the published release and verifying a recorded digest would also be
content-addressed and would avoid the submodule. It requires network access
during verification, or a cached artifact that becomes its own provenance
problem, and it reintroduces exactly the failure this replaces: a recorded
digest that nobody can reproduce once the procedure is forgotten.

### Use an off-the-shelf Markdown link checker

Several exist and handle more link syntax than a shell script does. None knows
about migration inventories, ADR index completeness, or friction and
failure-mode identifiers, which are the repository-specific invariants that
actually rot. Adding a dependency for the generic half while writing the
specific half anyway was not worth the pinning and supply-chain cost.

### Check only that identifiers exist, not that links resolve

Identifier checking alone would catch retired frictions but not the anchor
breakage that a heading retitle causes, which is the more common and more silent
failure. Both are cheap in the same pass.

# ADR-0035: Vendor the minimal ASVS baseline

- Status: Accepted
- Date: 2026-09-06
- Supersedes: The ASVS submodule portion of
  [ADR-0022](0022-verify-documentation-against-pinned-sources.md)

## Context

The ASVS evidence check reads only the English requirement chapters from OWASP
ASVS 5.0.0, but ADR-0022 pinned the complete upstream repository as a shallow
submodule. A checkout occupied roughly 159 MB because it included historical
standards, translations, images, and generated publications. The 17 files that
the check actually parsed total 167,549 bytes.

That arrangement made a recursive clone or a separate submodule initialization
a prerequisite for `security:check` and therefore for `devenv test`. It also
made both quality workflows fetch material that no verification consumed.

The replacement must retain the useful properties of the submodule: the input
must be available to every contributor, tied to an exact upstream release,
protected against unnoticed edits, licensed and attributed correctly, and
compared mechanically with every identifier and level in the assessment
profile. Routine verification must not depend on network access.

## Decision

Vendor an exact, unmodified snapshot of the 17 English requirement chapters
`5.0/en/0x10-*.md` through `5.0/en/0x26-*.md` from OWASP/ASVS tag
`v5.0.0_release`, commit
`5cf9b032440be53ce345ab3c130fda46ba1ce7a2`, under
`third_party/asvs/5.0/en`. Do not copy front matter, translations, historical
releases, images, mappings, or generated publications because the check does
not read them.

Preserve the upstream Creative Commons Attribution-ShareAlike 4.0 International
license as `third_party/asvs/LICENSE.md`. Keep the upstream URL, tag, commit,
scope, omission rationale, modification status, attribution, and reproducible
refresh procedure in `third_party/asvs/SOURCE.md`. A checked-in `SHA256SUMS`
manifest pins the license and every included chapter byte-for-byte.

`nix/asvs-source-check.sh` requires the exact 17-chapter inventory, verifies the
manifest, and requires both the source record and `docs/security/ASVS.md` to
name the expected tag and commit. It then extracts requirement identifiers and
levels only from the enumerated chapters and requires exact equality with the
checked-in assessment profile. A missing, added, or modified source file fails
the gate before the profile comparison.

The normal gate is offline. Provenance is reviewed when the snapshot changes:
the refresh procedure starts from a temporary checkout of the recorded upstream
release, regenerates the manifest, and requires review of every assessment
disposition. The recorded commit and reproducible extraction procedure provide
the external provenance; the manifest provides automated integrity after the
extract enters this repository.

Remove `.gitmodules` and the submodule checkout configuration from continuous
integration. Retain `third_party/**` in workflow path filters so a snapshot,
license, or provenance change always runs the quality gates.

## Consequences

- An ordinary clone contains every ASVS input needed by `security:check`; CI and
  contributors no longer fetch an unrelated 159 MB working tree.
- The unresolved large-checkout friction is resolved and removed from the
  friction register.
- The source check still detects profile drift and now also detects changes to
  the vendored text, license, or file inventory without relying on nested Git
  repository state.
- The repository now redistributes CC BY-SA material. The ASVS license and
  attribution are explicit and scoped to `third_party/asvs`; the absence of a
  license for the repository's own work remains tracked separately.
- A coordinated edit could change a vendored file and its manifest. Such a
  change is intentionally visible in the same review as the recorded release
  identity and source procedure; independently contacting upstream on every
  gate would sacrifice offline reproducibility.
- Moving to another ASVS release remains deliberately disruptive: the source
  record, checker pin, manifest, security narrative, and every affected
  disposition must change together.

## Alternatives considered

### Retain the submodule

This preserves Git-native provenance but keeps the large checkout and separate
initialization workflow that motivated the change.

### Configure sparse checkout inside the submodule

Sparse checkout could reduce the working tree, but it adds nested repository
configuration that ordinary submodule initialization and standard CI checkout
do not preserve uniformly. It also still requires a second repository fetch.

### Vendor only extracted identifier and level rows

A generated row-only artifact would be smaller, but it would no longer be an
exact upstream file. Reviewers would have to trust both an extraction program
and its output when checking attribution and provenance. Keeping the complete
17 chapter files is small enough and preserves byte-for-byte comparison.

### Download the release during every check

Fetching an archive and verifying a digest avoids checked-in source text, but
it makes the security gate depend on network and upstream availability. It also
does not remove the need to preserve attribution and a reproducible pin.

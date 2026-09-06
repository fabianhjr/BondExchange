# OWASP ASVS 5.0.0 source snapshot

This directory contains the unmodified English requirement chapters from the
[OWASP Application Security Verification Standard](https://github.com/OWASP/ASVS)
5.0.0 release. The snapshot is pinned to tag `v5.0.0_release`, commit
`5cf9b032440be53ce345ab3c130fda46ba1ce7a2`.

Only the 17 upstream files `5.0/en/0x10-*.md` through
`5.0/en/0x26-*.md` are included because those are the files from which
`nix/asvs-source-check.sh` extracts requirement identifiers and levels. The
front matter, translations, generated publications, mappings, images, and
historical releases are intentionally omitted. The included chapter files have
not been modified.

The ASVS material is by the OWASP Foundation and OWASP/ASVS contributors and is
licensed under the Creative Commons Attribution-ShareAlike 4.0 International
license. The upstream license text is preserved in `LICENSE.md`. That license
applies to the vendored ASVS material; it does not supply a license for the
rest of this repository.

`SHA256SUMS` pins the byte content of the upstream license and every included
chapter. `devenv tasks run security:check` verifies those digests, the exact
chapter inventory, the recorded tag and commit, and the equality of the source
requirement identifiers and levels with the local assessment profile. Normal
verification is entirely offline.

## Refresh procedure

To move to another ASVS release:

1. Clone `https://github.com/OWASP/ASVS.git` into a temporary directory, check
   out the intended release tag, and verify the resolved commit with
   `git rev-parse HEAD`.
2. Replace `LICENSE.md` and only the English requirement chapters used by the
   checker. Do not copy unrelated releases or generated assets.
3. From `third_party/asvs`, regenerate the manifest with
   `sha256sum LICENSE.md 5.0/en/0x{10..26}-*.md` and review the complete diff.
4. Update the tag and commit in this file, `nix/asvs-source-check.sh`, and
   `docs/security/ASVS.md`.
5. Review every profile disposition against the new requirement text before
   running the repository gates.

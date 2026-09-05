# Security policy

This repository is a bond-sale service backed by an executable specification.
It is **not production-ready**: it ships plaintext loopback listeners, no
production package, and no deployment architecture. See
[F-011](FRICTIONS.md#f-011--the-production-deployment-boundary-is-unspecified-p1)
and [FM-008](docs/FMEA.md#fm-008--unsafe-production-deployment-boundary) for
the controls that a deployment must still own. Report findings anyway: a defect
in the application-owned controls matters regardless of hosting status.

## Reporting a vulnerability

Report privately through GitHub's security advisory workflow on this
repository, under **Security → Report a vulnerability**. That channel keeps the
report and its discussion private until a fix is published.

Do not open a public issue for a suspected vulnerability, and do not include
credentials, tokens, or production data in a report. A proof of concept against
a local `devenv up` demo is sufficient and preferred.

Useful reports include the affected component, the trust boundary crossed, the
observed and expected behavior, and the steps to reproduce. The
[ASVS profile](docs/security/ASVS.md) records which controls this repository
claims to own; a report that a claimed control does not hold is especially
valuable.

## Response

The maintainer listed in [`.github/CODEOWNERS`](.github/CODEOWNERS) is
accountable for triage, remediation, and the advisory. Expect acknowledgement
within seven days. There is no bug bounty and no service-level agreement; this
is a single-maintainer repository.

Remediation targets are measured from first confirmation and are the same ones
the ASVS profile applies to dependency vulnerabilities:

| Severity | Maximum time from confirmation to remediation |
| --- | --- |
| Known exploitation or critical impact | 24 hours |
| High | 7 days |
| Medium | 30 days |
| Low | 90 days |

An unsupported component is high severity at minimum. An exception requires an
architecture decision record recording the compensating controls and an expiry
date.

## Supported versions

Only the `main` branch is supported. There are no releases, tags, or backports.

## Automated scanning

The [scheduled security workflow](.github/workflows/scheduled-security.yml)
runs `devenv tasks run security:check` every day at 06:00 UTC, and on demand
through workflow dispatch. It runs `govulncheck` against the locked Go module
graph, verifies the ASVS profile against its pinned upstream source, records
the module inventory as a retained artifact, and exercises the security-focused
tests. A failure opens or updates a tracking issue on this repository.

The daily cadence bounds the time from public disclosure of a Go dependency
vulnerability to its confirmation here, which is what makes the 24-hour
critical-remediation target measurable. The same check also runs on every pull
request and push through the Go quality workflow.

GitHub disables scheduled workflows in repositories with no activity for 60
days. A maintainer who intends the repository to stay idle must either
re-enable the schedule or accept that confirmation is no longer bounded, and
record that decision.

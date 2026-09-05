# ADR-0021: Schedule security scanning and name a response owner

- Status: Accepted
- Date: 2026-09-04

## Context

The ASVS profile commits to remediation targets measured from first
confirmation: 24 hours for known exploitation or critical impact, seven days
for high, 30 days for medium, and 90 days for low.

Confirmation itself had no bounded time. `govulncheck` ran only inside the Go
quality workflow, which triggers on workflow dispatch and on pushes or pull
requests matching its path filters. A newly disclosed vulnerability in a locked
Go dependency is not a change to this repository, so an idle repository could
carry a known-vulnerable dependency indefinitely and the 24-hour clock would
never start. The targets were therefore unmeasurable rather than demanding.

The repository also named no one. `docs/FMEA.md` states plainly that no system
owners are defined, and every high-priority action names an accountable area
instead of a person. A scan that fails with nobody accountable for reading the
result is not a detection control.

## Decision

Run `devenv tasks run security:check` on a schedule, daily at 06:00 UTC, and on
workflow dispatch, in `.github/workflows/scheduled-security.yml`. The daily
cadence is chosen to match the shortest remediation target: a 24-hour critical
window is only meaningful if confirmation cannot lag disclosure by more than
about a day.

Report failures durably rather than by notification alone. The job opens a
tracking issue titled "Scheduled security scan failed", reusing the open issue
if one exists so that a persistent failure produces one thread rather than a
daily pile. It retains the Go module inventory as an artifact for 90 days,
matching the longest remediation window, so a later investigation can
reconstruct the dependency graph as scanned.

Name an accountable maintainer in `.github/CODEOWNERS` and publish the
reporting channel, expectations, and remediation targets in `SECURITY.md`.
Vulnerability reports use GitHub's private advisory workflow; no contact email
is published, so no personal address enters a public file.

## Consequences

- Time from public disclosure of a Go dependency vulnerability to confirmation
  in this repository is bounded to roughly one day, which makes the ASVS
  remediation targets measurable.
- FM-014's detection score improves from 8 to 6. Severity and occurrence are
  unchanged: scanning bounds discovery for the defect classes it analyzes and
  does nothing for composition failure paths, which F-016 still tracks.
- A scan failure produces an owned, durable artifact — an issue on the
  repository — rather than an email that may go unread.
- GitHub disables scheduled workflows after 60 days of repository inactivity.
  An idle repository silently loses this control, which `SECURITY.md` states
  explicitly so that the loss is a recorded decision rather than a surprise.
- Naming a single maintainer is honest for a single-maintainer repository but
  is not the per-area ownership that a production-readiness decision requires;
  `CODEOWNERS` records that distinction.
- A daily job consumes CI minutes on a repository that may be idle. This is the
  intended trade: the cost is proportional to the schedule, not to activity,
  which is precisely why it detects what change-triggered scanning cannot.

## Alternatives considered

### Rely on Dependabot or GitHub's dependency alerts

These would detect vulnerable Go modules without a scheduled job, and would
also propose upgrades. They report against advisory metadata for the module
graph rather than against reachable call paths, so they neither replace
`govulncheck`'s reachability analysis nor exercise the ASVS evidence checks and
security-focused tests that `security:check` also runs. They remain a
reasonable future addition alongside this schedule, not instead of it.

### Scan weekly instead of daily

A weekly cadence would cut CI cost sevenfold but would allow confirmation to
lag disclosure by up to a week, which is longer than the high-severity
remediation target and far longer than the critical one. The schedule would
then contradict the published policy.

### Publish a security contact email

An email channel is simpler for reporters, but it would place a personal
address in a public file and provide no private discussion thread. GitHub's
advisory workflow keeps the report private until a fix is published and links
the eventual advisory to the repository.

### Fail the scheduled job without opening an issue

GitHub emails the maintainer when a scheduled workflow fails. That notification
is not retained, is easy to filter away, and leaves no record that the response
policy was engaged. An issue is the durable, auditable signal the policy needs.

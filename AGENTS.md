# archcheck

Public Go CLI for checking architectural constraints against a local CodeGraph index.

## Repository Fleet

Read `~/code/hq/FLOTILLA.md` at the start of a new session. This repository belongs
in personal-ventures; HQ owns the portfolio passport, this repository owns code,
requirements, tests, and development status. Never copy private project material here.

## Development

- Read README.md and docs/architecture.md before changing behavior.
- Keep the Go checker independent from CodeGraph's runtime; the adapter reads SQLite read-only.
- Do not describe the structural graph as a full CPG or promise complete dynamic-call resolution.
- Fail explicitly on invalid rules, incompatible schemas, stale indexes, and empty selectors.
- Default user-facing CLI text to Russian. Machine-readable fields and rule IDs remain English.
- Run go test ./..., go vet ./..., and git diff --check before handing off.
- Exercise the upstream integration against the pinned CodeGraph version in integration/package.json.
- Do not publish or push unless the user requests it. Do not change branches in the canonical checkout.

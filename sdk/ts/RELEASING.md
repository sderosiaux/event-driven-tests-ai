# Releasing

Workflow:

1. Update `sdk/ts/package.json` `version`.
2. Add a section to `sdk/ts/CHANGELOG.md` describing the change.
3. Commit: `chore(sdk/ts): release 0.X.Y`.
4. Tag: `git tag sdk-ts/v0.X.Y && git push --tags`.

The `sdk-ts-release` workflow picks up the tag, builds, runs the full test
suite (including round-trip against the Go binary), and publishes to npm
using the `NPM_TOKEN` repo secret and npm provenance.

## Prereqs (one-time)

- Add `NPM_TOKEN` as a GitHub Actions secret. Use a granular token scoped to
  the `@event-driven-tests-ai` scope with publish permission.
- Link the `@event-driven-tests-ai` npm scope to this GitHub repo for npm
  provenance attestations.

# Contributing to edt

Thanks for taking a look. This is a pre-alpha project, so the best contribution right now is usually a concrete scenario you can't test today. That tells us what to build next.

## Reporting a bug

Open an issue with:

- The scenario YAML that reproduces it (or a minimal cut-down version)
- The command you ran and the observed output
- What you expected to happen
- The edt version (`edt --version`) and Kafka flavor (Apache, Redpanda, Confluent Cloud, MSK, ...)

## Suggesting a feature

Issues describing a real testing pain are worth more than PRs that scratch a single itch. "I need to test X end-to-end and the current DSL can't express it" is the right shape. A paragraph beats a wishlist.

## Development setup

```bash
git clone https://github.com/event-driven-tests-ai/edt.git
cd edt
go test ./... -race -count=1   # unit suite
go build ./cmd/edt             # local binary at ./edt
```

For Postgres-backed integration tests you need Docker running:

```bash
go test -tags=integration ./...
```

## Pull requests

- One logical change per PR. Refactors and features in separate PRs.
- Tests required for any non-trivial change. TDD preferred; red-then-green beats "I tested it manually".
- Keep the commit message descriptive. We use Conventional Commits loosely (`feat:`, `fix:`, `chore:`, `docs:`) but don't police the exact shape.
- Run `go vet ./...` and `go test ./... -race` locally before pushing.
- Design docs in `docs/plans/` are the source of truth for what a milestone tried to solve. If you're changing something fundamental, update the relevant plan in the same PR.

## Code style

- Standard `gofmt` / `go vet`.
- Prefer stdlib over new dependencies; when adding one, say why in the commit message.
- Error messages in American English, lowercase, no punctuation. `fmt.Errorf("kafka: consume topic %q: %w", topic, err)` beats `"Failed to consume topic"`.
- No comments that restate what the code does. Comments are for *why* and for surprising constraints.

## License

By contributing you agree that your contributions are licensed under Apache 2.0. No CLA. No DCO required for small changes; we may ask for a `Signed-off-by:` line on larger patches.

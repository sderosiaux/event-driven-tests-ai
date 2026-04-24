# Changelog

## Unreleased

### Added
- `Scenario` types mirroring the Go data model
- `emit(scenario)` canonical YAML serializer
- `parse(yaml)` round-trip helper
- `scenario(name)` fluent `ScenarioBuilder` with camelCase API
- `edt-ts compile` CLI — compile TS scenario files to YAML
- Round-trip test suite against the `edt validate` binary

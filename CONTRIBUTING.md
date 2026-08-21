# Contributing to Mettle

Thanks for your interest in contributing! This guide covers the basics.

## Prerequisites

- Go 1.26+

## Running Tests

```bash
go test ./...
```

All tests run without API keys — the deterministic agent (`demo`) is used by default.

## Adding New Scenarios

1. Create a YAML file in `examples/scenarios/` following the existing structure
2. Define scenarios with:
   - `name`: unique identifier
   - `input`: the prompt or message
   - `expect.scope`: allowed tenants, domains, and tools
   - `expect.visibility`: `required` or `silent_ok`
3. Add test cases in the corresponding `_test.go` files if needed
4. Run the suite to verify:
   ```bash
   go run ./cmd/mettle run --spec examples/scenarios/your-scenario.yaml
   ```

## Adding New Metrics

1. Add the metric computation logic in `internal/metrics/metrics.go`
2. Register it in the `Compute` function
3. Add tests in `internal/metrics/metrics_test.go`

## Code Style

- Follow existing patterns in the codebase
- Keep functions small and focused
- Use table-driven tests where appropriate
- No external test dependencies (no testify, no testcontainers)
- fakes over mocks — use scripted providers and fake stores

## PR Process

1. Fork the repo and create a branch from `main`
2. Make your changes with tests
3. Run `go test ./...` to verify
4. Open a PR with a clear description of what changed and why
5. Keep PRs focused — one logical change per PR

## Architecture

Read [DECISIONS.md](./DECISIONS.md) for architecture decisions. Every significant choice has an ADR explaining the reasoning.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.

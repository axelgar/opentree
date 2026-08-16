# Contributing

Requires `golangci-lint` v2.12.2 (matching CI and the `version: "2"` config format of `.golangci.yml` — `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2`; a v1 binary misreads the v2 config and reports issues CI does not). Also `goimports` (`go install golang.org/x/tools/cmd/goimports@latest`); `govulncheck` and `deadcode` need no install — they run via `go tool`.

Before opening a PR, run:

```bash
make check
```

This runs, in order:

- `fmt` — `goimports`
- `lint` — `golangci-lint` (includes `govet`, `errcheck`, `staticcheck`, `gosec`, and more — see `.golangci.yml`)
- `vulncheck` — `govulncheck`, checks dependencies for known vulnerabilities
- `deadcode` — flags code unreachable from `cmd/opentree`
- `test` — `go test ./...`

CI runs the same checks on every push and PR (`.github/workflows/ci.yml`). This applies whether the change was written by a human or an AI coding agent — `make check` is the gate either way.

## Pre-commit hook

Run once after cloning to enforce `make check` on every commit:

```bash
make install-hooks
```

This points git at the tracked `.githooks/` directory (`git config core.hooksPath .githooks`). Skip a one-off commit with `git commit --no-verify`.
